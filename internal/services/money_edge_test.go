package services

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

func TestZeroAndFractionalQuantitiesStayExact(t *testing.T) {
	t.Parallel()
	database := valuationDatabase(t)
	prices := cachedPriceService(t, database)
	holdings := NewHoldingService(database, prices)

	_, err := database.ExecContext(t.Context(), `INSERT INTO holdings
(name, asset_type, quantity, price_provider, base_currency, manual_price) VALUES
('Closed', 'Cash', 0, 'Manual', 'TRY', 12.5),
('Fraction', 'Cash', 0.125, 'Manual', 'USD', 8),
('Dust', 'Cash', 1e-8, 'Manual', 'TRY', 100)`)
	if err != nil {
		t.Fatal(err)
	}

	rows := holdingRows(t, holdings, "TRY")
	closed, fraction, dust := rows["Closed"], rows["Fraction"], rows["Dust"]
	requirePricedZeroQuantity(t, &closed)
	requireFractionalUSD(t, &fraction)
	requireExactDust(t, &dust)

	snapshots := NewSnapshotService(database, prices, holdings)

	err = snapshots.CreateSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	history, err := snapshots.GetHoldingHistory(t.Context(), rows["Closed"].ID, 1)
	requirePricedZeroHistory(t, history, err)
}

func TestSameCurrencyTotalsIgnoreCrossRateRoundTrip(t *testing.T) {
	t.Parallel()
	database := valuationDatabase(t)

	const (
		usdTRY = 43.27
		eurTRY = 48.91
		euro   = 6.375
	)

	_, err := database.ExecContext(t.Context(), `UPDATE price_cache SET price = ?
WHERE provider = 'Frankfurter' AND instrument_id = 'USD'`, usdTRY)
	if err != nil {
		t.Fatal(err)
	}

	_, err = database.ExecContext(t.Context(), `UPDATE price_cache SET price = ?
WHERE provider = 'Frankfurter' AND instrument_id = 'EUR'`, eurTRY)
	if err != nil {
		t.Fatal(err)
	}

	_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
(name, asset_type, quantity, price_provider, base_currency, manual_price, est_price) VALUES
('Lira', 'Cash', 2, 'Manual', 'TRY', 100, 100),
('Dollar', 'Cash', 0.125, 'Manual', 'USD', 8, 8),
('Euro', 'Cash', 1, 'Manual', 'EUR', ?, ?)`, euro, euro)
	if err != nil {
		t.Fatal(err)
	}

	prices := cachedPriceService(t, database)

	rows := holdingRows(t, NewHoldingService(database, prices), "EUR")
	requireNativeQuotes(t, rows, euro)
	requireSingleFXConversion(t, rows, usdTRY, eurTRY, euro)

	data, err := NewHoldingService(database, prices).GetDashboardData(t.Context(), "EUR")
	if err != nil {
		t.Fatal(err)
	}

	wantEUR := rows["Lira"].NormTotalEUR + rows["Dollar"].NormTotalEUR + euro
	if data.CurrentValueEUR != wantEUR || data.EstimatedValueEUR != wantEUR {
		t.Fatalf("EUR gain: current %v estimated %v want %v",
			data.CurrentValueEUR, data.EstimatedValueEUR, wantEUR)
	}
}

func TestNonPositiveProviderPricesStayOutOfTotals(t *testing.T) {
	t.Parallel()

	for _, quote := range []float64{0, -10} {
		t.Run(strconv.FormatFloat(quote, 'f', -1, 64), func(t *testing.T) {
			t.Parallel()
			database := valuationDatabase(t)

			_, err := database.ExecContext(t.Context(), `INSERT INTO holdings
(name, asset_type, quantity, price_provider, instrument_id, base_currency, manual_price) VALUES
('Cash', 'Cash', 1, 'Manual', '', 'TRY', 100),
('Bad', 'Stock', 4, 'Yahoo Finance', 'BAD', 'USD', 0)`)
			if err != nil {
				t.Fatal(err)
			}

			_, err = database.ExecContext(t.Context(), `INSERT INTO price_cache
(provider, instrument_id, price, currency, source, expires_at, last_error)
VALUES ('Yahoo Finance', 'BAD', ?, 'USD', 'test', ?, '')`, quote, time.Now().Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}

			prices := cachedPriceService(t, database)
			rows := holdingRows(t, NewHoldingService(database, prices), "TRY")

			bad := rows["Bad"]
			if bad.ValuationAvailable || bad.NormTotalTRY != 0 || bad.NormTotalUSD != 0 ||
				bad.NormTotalEUR != 0 {
				t.Fatalf("non-positive quote entered the total: %+v", bad)
			}

			data, err := NewHoldingService(database, prices).GetDashboardData(t.Context(), "TRY")
			if err != nil || data.CurrentValueTRY != 100 || data.ValuationComplete ||
				data.UnvaluedCount != 1 {
				t.Fatalf("dashboard accepted non-positive quote: %+v %v", data, err)
			}

			err = NewSnapshotService(
				database,
				prices,
				NewHoldingService(database, prices),
			).CreateSnapshot(t.Context())
			if err == nil {
				t.Fatal("snapshot stored a portfolio with a non-positive provider price")
			}
		})
	}
}

func TestManualEUROverrideIgnoresProviderQuote(t *testing.T) {
	t.Parallel()
	database := valuationDatabase(t)

	_, err := database.ExecContext(t.Context(), `INSERT INTO holdings
(name, asset_type, quantity, price_provider, instrument_id, base_currency, manual_price)
VALUES ('Euro stock', 'Stock', 2, 'Yahoo Finance', 'EURTEST', 'EUR', 7)`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = database.ExecContext(t.Context(), `INSERT INTO price_cache
(provider, instrument_id, price, currency, source, expires_at, last_error)
VALUES ('Yahoo Finance', 'EURTEST', -100, 'USD', 'test', ?, '')`, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	row := holdingRows(t, NewHoldingService(database, cachedPriceService(t, database)), "EUR")["Euro stock"]
	if row.PriceSource != "Manual" || row.CurrentPrice != 7 || row.CurrentCurrency != "EUR" ||
		row.NormTotalEUR != 14 || row.NormTotalTRY != 700 || row.NormTotalUSD != 17.5 {
		t.Fatalf("manual EUR override: %+v", row)
	}
}

func TestMissingFXRateDoesNotSnapshotZero(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	registry := price.NewRegistry()
	registry.Register(offlineFXGetter{name: "Frankfurter"})
	registry.Register(offlineFXGetter{name: "Yahoo Finance"})
	registry.Register(price.NewManual())
	prices := price.NewService(database, registry)

	t.Cleanup(func() { _ = prices.Close() })

	holdings := NewHoldingService(database, prices)

	_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
(name, asset_type, quantity, price_provider, base_currency, manual_price)
VALUES ('Cash', 'Cash', 2, 'Manual', 'TRY', 100)`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = holdings.GetDashboardData(t.Context(), "TRY")
	if err == nil {
		t.Fatal("TRY cash was valued without a USD/TRY rate")
	}

	err = NewSnapshotService(database, prices, holdings).CreateSnapshot(t.Context())
	if err == nil {
		t.Fatal("missing FX rate persisted a snapshot")
	}

	var count int

	err = database.GetContext(t.Context(), &count, "SELECT COUNT(*) FROM snapshots")
	if err != nil || count != 0 {
		t.Fatalf("snapshots = %d, %v", count, err)
	}
}

func TestBaseCurrencyChangePreservesRecordedSnapshots(t *testing.T) {
	t.Parallel()
	database := valuationDatabase(t)
	prices := cachedPriceService(t, database)
	holdings := NewHoldingService(database, prices)
	snapshots := NewSnapshotService(database, prices, holdings)

	_, err := database.ExecContext(t.Context(), `INSERT INTO holdings
(name, asset_type, quantity, price_provider, base_currency, manual_price)
VALUES ('Cash', 'Cash', 2, 'Manual', 'TRY', 100)`)
	if err != nil {
		t.Fatal(err)
	}

	err = snapshots.CreateSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	currency := "USD"

	_, err = holdings.Patch(t.Context(), 1, &models.HoldingPatch{BaseCurrency: &currency})
	if err != nil {
		t.Fatal(err)
	}

	err = NewSettingsService(database).SetDisplayCurrency(t.Context(), "EUR")
	if err != nil {
		t.Fatal(err)
	}

	saved, err := snapshots.GetSnapshots(t.Context(), 10, 0, 0)
	requireOriginalTRYSnapshot(t, saved, err)

	data, err := holdings.GetDashboardData(t.Context(), "EUR")
	requireRebasedLiveTotals(t, data, err)

	err = snapshots.CreateSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	saved, err = snapshots.GetSnapshots(t.Context(), 10, 0, 0)
	if err != nil || len(saved) != 2 || saved[1].TotalValueTRY != 200 ||
		saved[0].TotalValueTRY != 8000 {
		t.Fatalf("old and new snapshots: %+v %v", saved, err)
	}

	_, err = database.ExecContext(t.Context(), `INSERT INTO snapshots
(timestamp, total_value_try, total_value_usd, usd_try_rate, holdings_json)
VALUES (datetime('now', '-1 hour'), 100, 2.5, 40, '[]')`)
	if err != nil {
		t.Fatal(err)
	}

	points, err := snapshots.GetSnapshotsForChart(t.Context(), 1)
	requireLegacyEURGap(t, points, err)
}

func TestLargeFiniteTotalsStayExact(t *testing.T) {
	t.Parallel()
	database := valuationDatabase(t)
	prices := cachedPriceService(t, database)

	_, err := database.ExecContext(t.Context(), `INSERT INTO holdings
(name, asset_type, quantity, price_provider, base_currency, manual_price, est_price)
VALUES ('Large', 'Cash', 1000000, 'Manual', 'TRY', 1000000, 1000000)`)
	if err != nil {
		t.Fatal(err)
	}

	row := holdingRows(t, NewHoldingService(database, prices), "TRY")["Large"]
	if row.NormTotalTRY != 1e12 || row.NormTotalUSD != 1e12/40 || row.NormEstTotalTRY != 1e12 {
		t.Fatalf("large total: %+v", row)
	}

	data, err := NewHoldingService(database, prices).GetDashboardData(t.Context(), "TRY")
	if err != nil || data.CurrentValueTRY != 1e12 || data.EstimatedValueTRY != 1e12 {
		t.Fatalf("large dashboard: %+v %v", data, err)
	}

	snapshots := NewSnapshotService(database, prices, NewHoldingService(database, prices))

	err = snapshots.CreateSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	saved, err := snapshots.GetSnapshots(t.Context(), 1, 0, 0)
	if err != nil || len(saved) != 1 || saved[0].TotalValueTRY != 1e12 ||
		saved[0].TotalValueUSD != 1e12/40 {
		t.Fatalf("large snapshot: %+v %v", saved, err)
	}
}

func requirePricedZeroQuantity(t *testing.T, row *models.HoldingWithPrice) {
	t.Helper()

	if !row.ValuationAvailable || row.IsFailed || row.NormTotalTRY != 0 ||
		row.NormTotalUSD != 0 || row.NormTotalEUR != 0 || row.CurrentPrice != 12.5 {
		t.Fatalf("priced zero quantity: %+v", row)
	}
}

func requireFractionalUSD(t *testing.T, row *models.HoldingWithPrice) {
	t.Helper()

	if row.NormTotalUSD != 1 || row.NormTotalTRY != 40 || row.NormTotalEUR != 0.8 ||
		row.CurrentCurrency != "USD" {
		t.Fatalf("fractional USD totals: %+v", row)
	}
}

func requireExactDust(t *testing.T, row *models.HoldingWithPrice) {
	t.Helper()

	if row.NormTotalTRY != row.Quantity*row.CurrentPrice || row.NormTotalTRY == 0 {
		t.Fatalf("dust total was rounded away in storage: %+v", row)
	}
}

func requirePricedZeroHistory(t *testing.T, history []HoldingHistoryPoint, err error) {
	t.Helper()

	if err != nil || len(history) != 1 || !history[0].ValuationAvailable || history[0].IsFailed ||
		history[0].ValueTRY != 0 || history[0].ValueUSD != 0 {
		t.Fatalf("priced zero history: %+v %v", history, err)
	}
}

func requireNativeQuotes(
	t *testing.T,
	rows map[string]models.HoldingWithPrice,
	euro float64,
) {
	t.Helper()

	if rows["Lira"].NormTotalTRY != 200 || rows["Dollar"].NormTotalUSD != 1 ||
		rows["Euro"].NormTotalEUR != euro || rows["Euro"].NormPriceEUR != euro {
		t.Fatalf("native totals were converted through FX: %+v", rows)
	}
}

func requireSingleFXConversion(
	t *testing.T,
	rows map[string]models.HoldingWithPrice,
	usdRate, eurRate, euroPrice float64,
) {
	t.Helper()

	// Parameters are float64, so this matches normalizePrice. Untyped constants
	// fold in arbitrary precision and land an ulp away.
	if rows["Dollar"].NormTotalTRY != 0.125*(8*usdRate) ||
		rows["Euro"].NormTotalTRY != euroPrice*eurRate ||
		rows["Lira"].NormTotalUSD != 2*(100/usdRate) ||
		rows["Lira"].NormTotalEUR != 2*(100/eurRate) {
		t.Fatalf("cross rates: %+v", rows)
	}
}

func requireOriginalTRYSnapshot(t *testing.T, saved []models.Snapshot, err error) {
	t.Helper()

	if err != nil || len(saved) != 1 ||
		saved[0].TotalValueTRY != 200 || saved[0].TotalValueUSD != 5 ||
		saved[0].USDTRYRate != 40 || saved[0].TotalValueEUR == nil ||
		*saved[0].TotalValueEUR != 4 || saved[0].EURTRYRate == nil ||
		*saved[0].EURTRYRate != 50 {
		t.Fatalf("history rewritten after base-currency change: %+v %v", saved, err)
	}
}

func requireRebasedLiveTotals(t *testing.T, data *models.DashboardData, err error) {
	t.Helper()

	if err != nil || data.CurrentValueUSD != 200 || data.CurrentValueTRY != 8000 ||
		data.CurrentValueEUR != 160 {
		t.Fatalf("live total did not follow the new base currency: %+v %v", data, err)
	}
}

func requireLegacyEURGap(t *testing.T, points []ChartPoint, err error) {
	t.Helper()

	if err != nil || len(points) != 3 || points[0].ValueEUR != nil {
		t.Fatalf("legacy EUR gap filled after display-currency change: %+v %v", points, err)
	}
}

func cachedPriceService(t *testing.T, database *sqlx.DB) *price.Service {
	t.Helper()

	prices := price.NewService(database, price.DefaultRegistry())

	t.Cleanup(func() { _ = prices.Close() })

	return prices
}

func holdingRows(
	t *testing.T,
	holdings *HoldingService,
	currency string,
) map[string]models.HoldingWithPrice {
	t.Helper()

	valued, err := holdings.GetWithPrices(t.Context(), currency)
	if err != nil {
		t.Fatal(err)
	}

	rows := make(map[string]models.HoldingWithPrice, len(valued))
	for i := range valued {
		rows[valued[i].Name] = valued[i]
	}

	return rows
}

type offlineFXGetter struct{ name string }

func (getter offlineFXGetter) Name() string { return getter.name }
func (offlineFXGetter) Type() string        { return "FX" }
func (offlineFXGetter) TTL() time.Duration  { return time.Minute }
func (offlineFXGetter) GetPrice(context.Context, string) (models.Price, error) {
	return models.Price{}, errTestEUROffline
}
