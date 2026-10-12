package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

var errUnsupportedQuoteCurrency = errors.New(
	"unsupported quote currency: only TRY and USD can be valued",
)

type HoldingService struct {
	db       *sqlx.DB
	priceSvc *price.Service
}

func NewHoldingService(database *sqlx.DB, priceSvc *price.Service) *HoldingService {
	return &HoldingService{db: database, priceSvc: priceSvc}
}

func (service *HoldingService) GetAll(ctx context.Context) ([]models.Holding, error) {
	var holdings []models.Holding

	err := service.db.SelectContext(
		ctx,
		&holdings,
		`SELECT updated_at, created_at, manual_price_at, price_provider, instrument_id, base_currency,
basket, location, notes, name, asset_type, ticker, manual_price, id, quantity, est_price FROM
holdings ORDER BY created_at DESC`,
	)

	return holdings, wrapOperationError("GetAll", err)
}

func (service *HoldingService) GetByID(ctx context.Context, id int) (*models.Holding, error) {
	var holding models.Holding

	err := service.db.GetContext(
		ctx,
		&holding,
		`SELECT updated_at, created_at, manual_price_at, price_provider, instrument_id, base_currency,
basket, location, notes, name, asset_type, ticker, manual_price, id, quantity, est_price FROM
holdings WHERE id = ?`,
		id,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // Absence is a successful lookup result.
	}

	return &holding, wrapOperationError("GetByID", err)
}

func (service *HoldingService) Create(ctx context.Context, holding *models.Holding) error {
	if holding.ManualPrice.Valid &&
		(holding.ManualPrice.Float64 < 0 || math.IsNaN(holding.ManualPrice.Float64) ||
			math.IsInf(holding.ManualPrice.Float64, 0)) {
		return errManualPriceMustBeFiniteAndNonnegative
	}

	now := time.Now().UTC()

	manualPrice := 0.0
	if holding.ManualPrice.Valid {
		manualPrice = holding.ManualPrice.Float64
	}

	holding.ManualPriceAt = sql.NullTime{Time: now, Valid: manualPrice > 0}
	holding.CreatedAt = now
	holding.UpdatedAt = now

	result, err := service.db.ExecContext(ctx, `
                INSERT INTO holdings (name, ticker, asset_type, quantity, price_provider,
instrument_id, base_currency, basket, location, notes, est_price, manual_price, manual_price_at,
created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		holding.Name,
		holding.Ticker,
		holding.AssetType,
		holding.Quantity,
		holding.PriceProvider,
		holding.InstrumentID,
		holding.BaseCurrency,
		holding.Basket,
		holding.Location,
		holding.Notes,
		holding.EstPrice,
		manualPrice,
		holding.ManualPriceAt,
		holding.CreatedAt,
		holding.UpdatedAt,
	)
	if err != nil {
		return wrapOperationError("Create", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return wrapOperationError("Create", err)
	}

	holding.ID = int(id)

	return nil
}

func (service *HoldingService) Update(ctx context.Context, holding *models.Holding) error {
	holding.UpdatedAt = time.Now().UTC()
	_, err := service.db.ExecContext(ctx, `
                UPDATE holdings SET name = ?, ticker = ?, asset_type = ?, quantity = ?,
price_provider = ?, instrument_id = ?, base_currency = ?, basket = ?, location = ?, notes = ?,
est_price = ?, updated_at = ?
		WHERE id = ?
	`,
		holding.Name,
		holding.Ticker,
		holding.AssetType,
		holding.Quantity,
		holding.PriceProvider,
		holding.InstrumentID,
		holding.BaseCurrency,
		holding.Basket,
		holding.Location,
		holding.Notes,
		holding.EstPrice,
		holding.UpdatedAt,
		holding.ID,
	)

	return wrapOperationError("Update", err)
}

func (service *HoldingService) UpdateEstPrice(ctx context.Context, id int, estPrice float64) error {
	_, err := service.db.ExecContext(ctx,
		"UPDATE holdings SET est_price = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		estPrice,
		id,
	)

	return wrapOperationError("UpdateEstPrice", err)
}

func (service *HoldingService) UpdateManualPrice(ctx context.Context, id int, quote float64) error {
	_, err := service.db.ExecContext(
		ctx,
		`UPDATE holdings SET manual_price = ?, manual_price_at = CURRENT_TIMESTAMP,
updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		quote,
		id,
	)

	return wrapOperationError("UpdateManualPrice", err)
}

func (service *HoldingService) ClearManualPrice(ctx context.Context, id int) error {
	_, err := service.db.ExecContext(
		ctx,
		"UPDATE holdings SET manual_price = 0, manual_price_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		id,
	)

	return wrapOperationError("ClearManualPrice", err)
}

func (service *HoldingService) Delete(ctx context.Context, id int) error {
	_, err := service.db.ExecContext(ctx, "DELETE FROM holdings WHERE id = ?", id)

	return wrapOperationError("Delete", err)
}

func (service *HoldingService) GetWithPrices(
	ctx context.Context,
	displayCurrency string,
) ([]models.HoldingWithPrice, error) {
	holdings, err := service.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	usdTryRate, err := service.priceSvc.GetUSDTTRYRate(ctx)
	if err != nil {
		return nil, wrapOperationError("GetWithPrices", err)
	}

	result := make([]models.HoldingWithPrice, 0, len(holdings))
	for index := range holdings {
		holding := holdings[index]
		hwp := models.HoldingWithPrice{Holding: holding}

		// A manual price overrides the configured automatic provider.
		if holding.ManualPrice.Valid && holding.ManualPrice.Float64 > 0 {
			hwp.CurrentPrice = holding.ManualPrice.Float64
			hwp.CurrentCurrency = holding.BaseCurrency
			hwp.PriceSource = "Manual"
			hwp.PriceFetchedAt = holding.ManualPriceAt.Time
		} else if holding.PriceProvider != "" && holding.PriceProvider != "Manual" && holding.InstrumentID != "" {
			quote, quoteErr := service.priceSvc.GetPrice(
				ctx,
				holding.PriceProvider,
				holding.InstrumentID,
			)
			if quoteErr == nil {
				hwp.CurrentPrice = quote.Value
				hwp.CurrentCurrency = quote.Currency
				hwp.PriceSource = quote.Source
				hwp.PriceFetchedAt = quote.FetchedAt
				hwp.IsStale = quote.IsStale
				hwp.IsFailed = quote.IsFailed
			} else {
				hwp.PriceError = quoteErr.Error()
				hwp.IsFailed = true
			}
		}

		if ctx.Err() != nil {
			return nil, wrapOperationError("GetWithPrices", ctx.Err())
		}

		if !canValueQuote(hwp.CurrentPrice, hwp.CurrentCurrency) {
			return nil, errUnsupportedQuoteCurrency
		}

		err = service.normalizeHolding(&hwp, usdTryRate)
		if err != nil {
			return nil, err
		}

		result = append(result, hwp)
	}

	return result, nil
}

var errNonFiniteHoldingValue = errors.New("non-finite holding valuation")

// A missing quote has zero value and needs no currency conversion.
func canValueQuote(value float64, currency string) bool {
	return value == 0 || currency == "TRY" || currency == "USD"
}

var errNonFiniteDashboardTotals = errors.New("non-finite dashboard totals")

func areFiniteValues(values ...float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}

	return true
}

func (service *HoldingService) GetDashboardData(
	ctx context.Context,
	displayCurrency string,
) (*models.DashboardData, error) {
	holdings, err := service.GetWithPrices(ctx, displayCurrency)
	if err != nil {
		return nil, err
	}

	var totalTRY, totalUSD, estTotalTRY, estTotalUSD float64

	staleCount, failedCount, coverageCount := 0, 0, 0
	allocType := make(map[string]float64)
	allocBasket := make(map[string]float64)
	allocLoc := make(map[string]float64)
	allocCurr := make(map[string]float64)

	for index := range holdings {
		holding := holdings[index]
		// Currency-specific totals must not depend on the allocation display currency.
		totalTRY += holding.NormTotalTRY
		totalUSD += holding.NormTotalUSD
		estTotalTRY += holding.NormEstTotalTRY
		estTotalUSD += holding.NormEstTotalUSD

		if holding.IsStale {
			staleCount++
		}

		if holding.IsFailed {
			failedCount++
		}

		if holding.EstPrice > 0 {
			coverageCount++
		}

		// Allocations based on current value.
		val := holding.NormTotalTRY
		if displayCurrency == "USD" {
			val = holding.NormTotalUSD
		}

		if val > 0 {
			allocType[string(holding.AssetType)] += val
			if holding.Basket != "" {
				allocBasket[holding.Basket] += val
			}

			if holding.Location != "" {
				allocLoc[holding.Location] += val
			}

			allocCurr[holding.BaseCurrency] += val
		}
	}

	if !areFiniteValues(totalTRY, totalUSD, estTotalTRY, estTotalUSD) {
		return nil, errNonFiniteDashboardTotals
	}

	providerHealth, err := service.priceSvc.GetProviderHealthContext(ctx)
	if err != nil {
		return nil, wrapOperationError("GetDashboardData", err)
	}

	usdTryRate, err := service.priceSvc.GetUSDTTRYRate(ctx)
	if err != nil {
		return nil, wrapOperationError("GetDashboardData", err)
	}

	return &models.DashboardData{
		CurrentValueTRY:    totalTRY,
		CurrentValueUSD:    totalUSD,
		EstimatedValueTRY:  estTotalTRY,
		EstimatedValueUSD:  estTotalUSD,
		USDTRYRate:         usdTryRate,
		StaleCount:         staleCount,
		FailedCount:        failedCount,
		CoverageCount:      coverageCount,
		TotalHoldings:      len(holdings),
		LastUpdated:        time.Now().UTC(),
		AllocationByType:   allocType,
		AllocationByBasket: allocBasket,
		AllocationByLoc:    allocLoc,
		AllocationByCurr:   allocCurr,
		ProviderHealth:     providerHealth,
	}, nil
}

func (service *HoldingService) GetUniqueBaskets(ctx context.Context) ([]string, error) {
	var baskets []string

	err := service.db.SelectContext(
		ctx,
		&baskets,
		"SELECT DISTINCT basket FROM holdings WHERE basket != '' ORDER BY basket",
	)

	return baskets, wrapOperationError("GetUniqueBaskets", err)
}

func (service *HoldingService) GetUniqueLocations(ctx context.Context) ([]string, error) {
	var locations []string

	err := service.db.SelectContext(
		ctx,
		&locations,
		"SELECT DISTINCT location FROM holdings WHERE location != '' ORDER BY location",
	)

	return locations, wrapOperationError("GetUniqueLocations", err)
}

var errNonFiniteSnapshotTotals = errors.New("non-finite snapshot totals")

type SnapshotService struct {
	db         *sqlx.DB
	priceSvc   *price.Service
	holdingSvc *HoldingService
}

func NewSnapshotService(
	database *sqlx.DB,
	priceSvc *price.Service,
	holdingSvc *HoldingService,
) *SnapshotService {
	return &SnapshotService{db: database, priceSvc: priceSvc, holdingSvc: holdingSvc}
}

func (service *SnapshotService) CreateSnapshot(ctx context.Context) error {
	holdings, err := service.holdingSvc.GetWithPrices(ctx, "TRY")
	if err != nil {
		return err
	}

	usdTryRate, err := service.priceSvc.GetUSDTTRYRate(ctx)
	if err != nil {
		return wrapOperationError("CreateSnapshot", err)
	}

	var totalTRY, totalUSD float64

	staleCount, failedCount := 0, 0

	snapshotHoldings := make([]map[string]any, 0, len(holdings))
	for index := range holdings {
		holding := holdings[index]
		valTRY := holding.NormTotalTRY
		valUSD := holding.NormTotalUSD
		totalTRY += valTRY
		totalUSD += valUSD

		if holding.IsStale {
			staleCount++
		}

		if holding.IsFailed {
			failedCount++
		}

		snapshotHoldings = append(snapshotHoldings, map[string]any{
			"id":        holding.ID,
			"name":      holding.Name,
			"quantity":  holding.Quantity,
			"price":     holding.CurrentPrice,
			"currency":  holding.CurrentCurrency,
			"value_try": valTRY,
			"value_usd": valUSD,
			"is_stale":  holding.IsStale,
			"is_failed": holding.IsFailed,
		})
	}

	if math.IsNaN(totalTRY) || math.IsInf(totalTRY, 0) ||
		math.IsNaN(totalUSD) || math.IsInf(totalUSD, 0) {
		return errNonFiniteSnapshotTotals
	}

	holdingsJSON, err := json.Marshal(snapshotHoldings)
	if err != nil {
		return wrapOperationError("CreateSnapshot", err)
	}

	_, err = service.db.ExecContext(ctx, `
                INSERT INTO snapshots (timestamp, total_value_try, total_value_usd,
usd_try_rate, stale_count, failed_count, holdings_json)
		VALUES (CURRENT_TIMESTAMP, ?, ?, ?, ?, ?, ?)
	`,
		totalTRY,
		totalUSD,
		usdTryRate,
		staleCount,
		failedCount,
		string(holdingsJSON),
	)

	return wrapOperationError("CreateSnapshot", err)
}

func (service *SnapshotService) GetSnapshots(
	ctx context.Context,
	limit, offset, days int,
) ([]models.Snapshot, error) {
	var snapshots []models.Snapshot

	err := service.db.SelectContext(
		ctx,
		&snapshots,
		`SELECT timestamp, created_at, holdings_json, id, total_value_try, total_value_usd, usd_try_rate,
stale_count, failed_count FROM snapshots WHERE (? = 0 OR timestamp >= ?)
         ORDER BY timestamp DESC LIMIT ? OFFSET ?`,
		days,
		time.Now().UTC().AddDate(0, 0, -days),
		limit,
		offset,
	)

	return snapshots, wrapOperationError("GetSnapshots", err)
}

func (service *SnapshotService) GetSnapshotsForChart(
	ctx context.Context,
	days int,
) ([]ChartPoint, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -days)

	var snapshots []models.Snapshot

	err := service.db.SelectContext(
		ctx,
		&snapshots,
		`SELECT timestamp, created_at, holdings_json, id, total_value_try, total_value_usd, usd_try_rate,
stale_count, failed_count FROM snapshots WHERE timestamp >= ? ORDER BY timestamp ASC`,
		cutoff,
	)
	if err != nil {
		return nil, wrapOperationError("GetSnapshotsForChart", err)
	}

	points := make([]ChartPoint, 0, len(snapshots))
	for _, snap := range snapshots {
		points = append(points, ChartPoint{
			Timestamp:   snap.Timestamp,
			ValueTRY:    snap.TotalValueTRY,
			ValueUSD:    snap.TotalValueUSD,
			USDTRYRate:  snap.USDTRYRate,
			StaleCount:  snap.StaleCount,
			FailedCount: snap.FailedCount,
		})
	}

	return points, nil
}

func (service *SnapshotService) GetHoldingHistory(
	ctx context.Context,
	holdingID, days int,
) ([]HoldingHistoryPoint, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -days)

	var snapshots []models.Snapshot

	err := service.db.SelectContext(
		ctx,
		&snapshots,
		`SELECT timestamp, created_at, holdings_json, id, total_value_try, total_value_usd, usd_try_rate,
stale_count, failed_count FROM snapshots WHERE timestamp >= ? ORDER BY timestamp ASC`,
		cutoff,
	)
	if err != nil {
		return nil, wrapOperationError("GetHoldingHistory", err)
	}

	points := make([]HoldingHistoryPoint, 0)

	for _, snap := range snapshots {
		var holdings []map[string]json.RawMessage

		err := json.Unmarshal([]byte(snap.HoldingsJSON), &holdings)
		if err != nil {
			continue
		}

		for _, holding := range holdings {
			var id int

			idErr := json.Unmarshal(holding["id"], &id)
			if idErr != nil {
				continue
			}

			if id == holdingID {
				var valTRY, valUSD float64

				valueErr := json.Unmarshal(holding["value_try"], &valTRY)
				if valueErr != nil {
					valTRY = 0
				}

				valueErr = json.Unmarshal(holding["value_usd"], &valUSD)
				if valueErr != nil {
					valUSD = 0
				}

				points = append(points, HoldingHistoryPoint{
					Timestamp: snap.Timestamp,
					ValueTRY:  valTRY,
					ValueUSD:  valUSD,
				})

				break
			}
		}
	}

	return points, nil
}

type ChartPoint struct {
	Timestamp   time.Time `json:"timestamp"`
	ValueTRY    float64   `json:"value_try"`
	ValueUSD    float64   `json:"value_usd"`
	USDTRYRate  float64   `json:"usd_try_rate"`
	StaleCount  int       `json:"stale_count"`
	FailedCount int       `json:"failed_count"`
}

type HoldingHistoryPoint struct {
	Timestamp time.Time `json:"timestamp"`
	ValueTRY  float64   `json:"value_try"`
	ValueUSD  float64   `json:"value_usd"`
}

type SettingsService struct {
	db *sqlx.DB
}

func NewSettingsService(database *sqlx.DB) *SettingsService {
	return &SettingsService{db: database}
}

func (service *SettingsService) GetAll(ctx context.Context) (models.Settings, error) {
	var settings models.Settings

	currency, err := db.GetSettingContext(ctx, service.db, "display_currency")
	if err != nil {
		return settings, wrapOperationError("GetAll", err)
	}

	settings.DisplayCurrency = currency
	if settings.DisplayCurrency == "" {
		settings.DisplayCurrency = "TRY"
	}

	mode, err := db.GetSettingContext(ctx, service.db, "dark_mode")
	if err != nil {
		return settings, wrapOperationError("GetAll", err)
	}

	settings.DarkMode = mode
	if settings.DarkMode == "" {
		settings.DarkMode = "system"
	}

	refreshStr, err := db.GetSettingContext(ctx, service.db, "refresh_interval")
	if err != nil {
		return settings, wrapOperationError("GetAll", err)
	}

	settings.RefreshInterval = 60

	interval, parseErr := strconv.Atoi(refreshStr)
	if parseErr == nil && interval >= 10 && interval <= 3600 {
		settings.RefreshInterval = interval
	}

	return settings, nil
}

func (service *SettingsService) SetDisplayCurrency(ctx context.Context, currency string) error {
	return wrapOperationError(
		"SetDisplayCurrency",
		db.SetSettingContext(ctx, service.db, "display_currency", currency),
	)
}

func (service *SettingsService) SetDarkMode(ctx context.Context, mode string) error {
	return wrapOperationError(
		"SetDarkMode",
		db.SetSettingContext(ctx, service.db, "dark_mode", mode),
	)
}

func (service *SettingsService) SetRefreshInterval(ctx context.Context, interval int) error {
	return wrapOperationError(
		"SetRefreshInterval",
		db.SetSettingContext(ctx, service.db, "refresh_interval", strconv.Itoa(interval)),
	)
}

var errManualPriceMustBeFiniteAndNonnegative = errors.New(
	"manual price must be finite and nonnegative",
)

func (service *HoldingService) normalizeHolding(
	holding *models.HoldingWithPrice,
	rate float64,
) error {
	holding.NormPriceTRY, holding.NormPriceUSD = service.normalizePrice(
		holding.CurrentPrice,
		holding.CurrentCurrency,
		rate,
	)
	holding.NormTotalTRY = holding.Quantity * holding.NormPriceTRY
	holding.NormTotalUSD = holding.Quantity * holding.NormPriceUSD

	if holding.EstPrice > 0 {
		estTRY, estUSD := service.normalizePrice(holding.EstPrice, holding.BaseCurrency, rate)
		holding.NormEstTotalTRY = holding.Quantity * estTRY
		holding.NormEstTotalUSD = holding.Quantity * estUSD
	}

	if !areFiniteValues(
		holding.NormPriceTRY,
		holding.NormPriceUSD,
		holding.NormTotalTRY,
		holding.NormTotalUSD,
		holding.NormEstTotalTRY,
		holding.NormEstTotalUSD,
	) {
		return errNonFiniteHoldingValue
	}

	return nil
}

func (service *HoldingService) normalizePrice(
	quote float64,
	currency string,
	usdTryRate float64,
) (float64, float64) {
	var try, usd float64

	switch currency {
	case "TRY":
		try = quote
		if usdTryRate > 0 {
			usd = quote / usdTryRate
		}
	case "USD":
		usd = quote
		if usdTryRate > 0 {
			try = quote * usdTryRate
		}
	default:
		try = quote
		if usdTryRate > 0 {
			usd = quote / usdTryRate
		}
	}

	return try, usd
}
