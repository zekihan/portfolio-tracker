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

func NewHoldingService(db *sqlx.DB, priceSvc *price.Service) *HoldingService {
	return &HoldingService{db: db, priceSvc: priceSvc}
}

func (s *HoldingService) GetAll(ctx context.Context) ([]models.Holding, error) {
	var holdings []models.Holding

	err := s.db.SelectContext(ctx, &holdings, "SELECT * FROM holdings ORDER BY created_at DESC")

	return holdings, err
}

func (s *HoldingService) GetByID(ctx context.Context, id int) (*models.Holding, error) {
	var h models.Holding

	err := s.db.GetContext(ctx, &h, "SELECT * FROM holdings WHERE id = ?", id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}

	return &h, err
}

func (s *HoldingService) Create(ctx context.Context, h *models.Holding) error {
	if h.ManualPrice.Valid &&
		(h.ManualPrice.Float64 < 0 || math.IsNaN(h.ManualPrice.Float64) || math.IsInf(h.ManualPrice.Float64, 0)) {
		return errors.New("manual price must be finite and nonnegative")
	}

	now := time.Now().UTC()

	manualPrice := 0.0
	if h.ManualPrice.Valid {
		manualPrice = h.ManualPrice.Float64
	}

	h.ManualPriceAt = sql.NullTime{Time: now, Valid: manualPrice > 0}
	h.CreatedAt = now
	h.UpdatedAt = now

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO holdings (name, ticker, asset_type, quantity, price_provider, instrument_id, base_currency, basket, location, notes, est_price, manual_price, manual_price_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, h.Name, h.Ticker, h.AssetType, h.Quantity, h.PriceProvider, h.InstrumentID, h.BaseCurrency, h.Basket, h.Location, h.Notes, h.EstPrice, manualPrice, h.ManualPriceAt, h.CreatedAt, h.UpdatedAt)
	if err != nil {
		return err
	}

	id, _ := result.LastInsertId()
	h.ID = int(id)

	return nil
}

func (s *HoldingService) Update(ctx context.Context, h *models.Holding) error {
	h.UpdatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		UPDATE holdings SET name = ?, ticker = ?, asset_type = ?, quantity = ?, price_provider = ?, instrument_id = ?, base_currency = ?, basket = ?, location = ?, notes = ?, est_price = ?, updated_at = ?
		WHERE id = ?
	`, h.Name, h.Ticker, h.AssetType, h.Quantity, h.PriceProvider, h.InstrumentID, h.BaseCurrency, h.Basket, h.Location, h.Notes, h.EstPrice, h.UpdatedAt, h.ID)

	return err
}

func (s *HoldingService) UpdateEstPrice(ctx context.Context, id int, estPrice float64) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE holdings SET est_price = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		estPrice,
		id,
	)

	return err
}

func (s *HoldingService) UpdateManualPrice(ctx context.Context, id int, price float64) error {
	_, err := s.db.ExecContext(
		ctx,
		"UPDATE holdings SET manual_price = ?, manual_price_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		price,
		id,
	)

	return err
}

func (s *HoldingService) ClearManualPrice(ctx context.Context, id int) error {
	_, err := s.db.ExecContext(
		ctx,
		"UPDATE holdings SET manual_price = 0, manual_price_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		id,
	)

	return err
}

func (s *HoldingService) Delete(ctx context.Context, id int) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM holdings WHERE id = ?", id)

	return err
}

func (s *HoldingService) GetWithPrices(
	ctx context.Context,
	displayCurrency string,
) ([]models.HoldingWithPrice, error) {
	holdings, err := s.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	usdTryRate, err := s.priceSvc.GetUSDTTRYRate(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]models.HoldingWithPrice, 0, len(holdings))
	for _, h := range holdings {
		hwp := models.HoldingWithPrice{Holding: h}

		// A manual price overrides the configured automatic provider.
		if h.ManualPrice.Valid && h.ManualPrice.Float64 > 0 {
			hwp.CurrentPrice = h.ManualPrice.Float64
			hwp.CurrentCurrency = h.BaseCurrency
			hwp.PriceSource = "Manual"
			hwp.PriceFetchedAt = h.ManualPriceAt.Time
		} else if h.PriceProvider != "" && h.PriceProvider != "Manual" && h.InstrumentID != "" {
			price, err := s.priceSvc.GetPrice(ctx, h.PriceProvider, h.InstrumentID)
			if err == nil {
				hwp.CurrentPrice = price.Value
				hwp.CurrentCurrency = price.Currency
				hwp.PriceSource = price.Source
				hwp.PriceFetchedAt = price.FetchedAt
				hwp.IsStale = price.IsStale
				hwp.IsFailed = price.IsFailed
			} else {
				hwp.PriceError = err.Error()
				hwp.IsFailed = true
			}
		}

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if !canValueQuote(hwp.CurrentPrice, hwp.CurrentCurrency) {
			return nil, errUnsupportedQuoteCurrency
		}

		err = s.normalizeHolding(&hwp, usdTryRate)
		if err != nil {
			return nil, err
		}

		result = append(result, hwp)
	}

	return result, nil
}

var errNonFiniteHoldingValue = errors.New("non-finite holding valuation")

func (s *HoldingService) normalizeHolding(h *models.HoldingWithPrice, rate float64) error {
	h.NormPriceTRY, h.NormPriceUSD = s.normalizePrice(h.CurrentPrice, h.CurrentCurrency, rate)
	h.NormTotalTRY = h.Quantity * h.NormPriceTRY
	h.NormTotalUSD = h.Quantity * h.NormPriceUSD

	if h.EstPrice > 0 {
		estTRY, estUSD := s.normalizePrice(h.EstPrice, h.BaseCurrency, rate)
		h.NormEstTotalTRY = h.Quantity * estTRY
		h.NormEstTotalUSD = h.Quantity * estUSD
	}

	if !areFiniteValues(h.NormPriceTRY, h.NormPriceUSD, h.NormTotalTRY, h.NormTotalUSD,
		h.NormEstTotalTRY, h.NormEstTotalUSD) {
		return errNonFiniteHoldingValue
	}

	return nil
}

// A missing quote has zero value and needs no currency conversion.
func canValueQuote(value float64, currency string) bool {
	return value == 0 || currency == "TRY" || currency == "USD"
}

func (s *HoldingService) normalizePrice(
	price float64,
	currency string,
	usdTryRate float64,
) (try, usd float64) {
	switch currency {
	case "TRY":
		try = price
		if usdTryRate > 0 {
			usd = price / usdTryRate
		}
	case "USD":
		usd = price
		if usdTryRate > 0 {
			try = price * usdTryRate
		}
	default:
		try = price
		if usdTryRate > 0 {
			usd = price / usdTryRate
		}
	}

	return try, usd
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

func (s *HoldingService) GetDashboardData(
	ctx context.Context,
	displayCurrency string,
) (*models.DashboardData, error) {
	holdings, err := s.GetWithPrices(ctx, displayCurrency)
	if err != nil {
		return nil, err
	}

	var totalTRY, totalUSD, estTotalTRY, estTotalUSD float64

	staleCount, failedCount, coverageCount := 0, 0, 0
	allocType := make(map[string]float64)
	allocBasket := make(map[string]float64)
	allocLoc := make(map[string]float64)
	allocCurr := make(map[string]float64)

	for _, h := range holdings {
		// Currency-specific totals must not depend on the allocation display currency.
		totalTRY += h.NormTotalTRY
		totalUSD += h.NormTotalUSD
		estTotalTRY += h.NormEstTotalTRY
		estTotalUSD += h.NormEstTotalUSD

		if h.IsStale {
			staleCount++
		}

		if h.IsFailed {
			failedCount++
		}

		if h.EstPrice > 0 {
			coverageCount++
		}

		// Allocations based on current value.
		val := h.NormTotalTRY
		if displayCurrency == "USD" {
			val = h.NormTotalUSD
		}

		if val > 0 {
			allocType[string(h.AssetType)] += val
			if h.Basket != "" {
				allocBasket[h.Basket] += val
			}

			if h.Location != "" {
				allocLoc[h.Location] += val
			}

			allocCurr[h.BaseCurrency] += val
		}
	}

	if !areFiniteValues(totalTRY, totalUSD, estTotalTRY, estTotalUSD) {
		return nil, errNonFiniteDashboardTotals
	}

	providerHealth, err := s.priceSvc.GetProviderHealthContext(ctx)
	if err != nil {
		return nil, err
	}

	usdTryRate, err := s.priceSvc.GetUSDTTRYRate(ctx)
	if err != nil {
		return nil, err
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

func (s *HoldingService) GetUniqueBaskets(ctx context.Context) ([]string, error) {
	var baskets []string

	err := s.db.SelectContext(
		ctx,
		&baskets,
		"SELECT DISTINCT basket FROM holdings WHERE basket != '' ORDER BY basket",
	)

	return baskets, err
}

func (s *HoldingService) GetUniqueLocations(ctx context.Context) ([]string, error) {
	var locations []string

	err := s.db.SelectContext(
		ctx,
		&locations,
		"SELECT DISTINCT location FROM holdings WHERE location != '' ORDER BY location",
	)

	return locations, err
}

var errNonFiniteSnapshotTotals = errors.New("non-finite snapshot totals")

type SnapshotService struct {
	db         *sqlx.DB
	priceSvc   *price.Service
	holdingSvc *HoldingService
}

func NewSnapshotService(
	db *sqlx.DB,
	priceSvc *price.Service,
	holdingSvc *HoldingService,
) *SnapshotService {
	return &SnapshotService{db: db, priceSvc: priceSvc, holdingSvc: holdingSvc}
}

func (s *SnapshotService) CreateSnapshot(ctx context.Context) error {
	holdings, err := s.holdingSvc.GetWithPrices(ctx, "TRY")
	if err != nil {
		return err
	}

	usdTryRate, err := s.priceSvc.GetUSDTTRYRate(ctx)
	if err != nil {
		return err
	}

	var totalTRY, totalUSD float64

	staleCount, failedCount := 0, 0

	snapshotHoldings := make([]map[string]any, 0, len(holdings))
	for _, h := range holdings {
		valTRY := h.NormTotalTRY
		valUSD := h.NormTotalUSD
		totalTRY += valTRY
		totalUSD += valUSD

		if h.IsStale {
			staleCount++
		}

		if h.IsFailed {
			failedCount++
		}

		snapshotHoldings = append(snapshotHoldings, map[string]any{
			"id":        h.ID,
			"name":      h.Name,
			"quantity":  h.Quantity,
			"price":     h.CurrentPrice,
			"currency":  h.CurrentCurrency,
			"value_try": valTRY,
			"value_usd": valUSD,
			"is_stale":  h.IsStale,
			"is_failed": h.IsFailed,
		})
	}

	if math.IsNaN(totalTRY) || math.IsInf(totalTRY, 0) ||
		math.IsNaN(totalUSD) || math.IsInf(totalUSD, 0) {
		return errNonFiniteSnapshotTotals
	}

	holdingsJSON, err := json.Marshal(snapshotHoldings)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(`
		INSERT INTO snapshots (timestamp, total_value_try, total_value_usd, usd_try_rate, stale_count, failed_count, holdings_json)
		VALUES (CURRENT_TIMESTAMP, ?, ?, ?, ?, ?, ?)
	`, totalTRY, totalUSD, usdTryRate, staleCount, failedCount, string(holdingsJSON))

	return err
}

func (s *SnapshotService) GetSnapshots(
	ctx context.Context,
	limit, offset, days int,
) ([]models.Snapshot, error) {
	var snapshots []models.Snapshot

	err := s.db.SelectContext(
		ctx,
		&snapshots,
		`SELECT * FROM snapshots WHERE (? = 0 OR timestamp >= ?)
         ORDER BY timestamp DESC LIMIT ? OFFSET ?`,
		days,
		time.Now().UTC().AddDate(0, 0, -days),
		limit,
		offset,
	)

	return snapshots, err
}

func (s *SnapshotService) GetSnapshotsForChart(
	ctx context.Context,
	days int,
) ([]ChartPoint, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -days)

	var snapshots []models.Snapshot

	err := s.db.SelectContext(
		ctx,
		&snapshots,
		"SELECT * FROM snapshots WHERE timestamp >= ? ORDER BY timestamp ASC",
		cutoff,
	)
	if err != nil {
		return nil, err
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

func (s *SnapshotService) GetHoldingHistory(
	ctx context.Context,
	holdingID, days int,
) ([]HoldingHistoryPoint, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -days)

	var snapshots []models.Snapshot

	err := s.db.SelectContext(
		ctx,
		&snapshots,
		"SELECT * FROM snapshots WHERE timestamp >= ? ORDER BY timestamp ASC",
		cutoff,
	)
	if err != nil {
		return nil, err
	}

	points := make([]HoldingHistoryPoint, 0)

	for _, snap := range snapshots {
		var holdings []map[string]json.RawMessage

		err := json.Unmarshal([]byte(snap.HoldingsJSON), &holdings)
		if err != nil {
			continue
		}

		for _, h := range holdings {
			var id int

			if idErr := json.Unmarshal(h["id"], &id); idErr != nil {
				continue
			}

			if id == holdingID {
				var valTRY, valUSD float64

				_ = json.Unmarshal(h["value_try"], &valTRY)
				_ = json.Unmarshal(h["value_usd"], &valUSD)

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

func NewSettingsService(db *sqlx.DB) *SettingsService {
	return &SettingsService{db: db}
}

func (s *SettingsService) GetAll(ctx context.Context) (models.Settings, error) {
	var settings models.Settings

	currency, err := db.GetSettingContext(ctx, s.db, "display_currency")
	if err != nil {
		return settings, err
	}

	settings.DisplayCurrency = currency
	if settings.DisplayCurrency == "" {
		settings.DisplayCurrency = "TRY"
	}

	mode, err := db.GetSettingContext(ctx, s.db, "dark_mode")
	if err != nil {
		return settings, err
	}

	settings.DarkMode = mode
	if settings.DarkMode == "" {
		settings.DarkMode = "system"
	}

	refreshStr, err := db.GetSettingContext(ctx, s.db, "refresh_interval")
	if err != nil {
		return settings, err
	}

	settings.RefreshInterval = 60

	interval, parseErr := strconv.Atoi(refreshStr)
	if parseErr == nil && interval >= 10 && interval <= 3600 {
		settings.RefreshInterval = interval
	}

	return settings, nil
}

func (s *SettingsService) SetDisplayCurrency(ctx context.Context, currency string) error {
	return db.SetSettingContext(ctx, s.db, "display_currency", currency)
}

func (s *SettingsService) SetDarkMode(ctx context.Context, mode string) error {
	return db.SetSettingContext(ctx, s.db, "dark_mode", mode)
}

func (s *SettingsService) SetRefreshInterval(ctx context.Context, interval int) error {
	return db.SetSettingContext(ctx, s.db, "refresh_interval", strconv.Itoa(interval))
}
