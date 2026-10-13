package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

func seedEURQuote(t *testing.T, database *sqlx.DB) {
	t.Helper()

	_, err := database.ExecContext(t.Context(), `INSERT OR REPLACE INTO price_cache
(provider, instrument_id, price, currency, source, expires_at, last_error)
VALUES ('Frankfurter', 'EUR', 50, 'TRY', 'test', ?, '')`, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
}

//nolint:gocyclo // End-to-end three-currency totals and persisted history contract.
func TestEURValuationAndHistory(t *testing.T) {
	t.Parallel()
	database := valuationDatabase(t)

	_, err := database.ExecContext(t.Context(), `INSERT INTO holdings
(name, asset_type, quantity, price_provider, base_currency, manual_price, est_price, basket, location)
VALUES ('Lira','Cash',2,'Manual','TRY',100,120,'Savings','Bank'),
('Dollar','Cash',3,'Manual','USD',10,20,'Savings','Bank'),
('Euro','Cash',4,'Manual','EUR',5,10,'Savings','Bank')`)
	if err != nil {
		t.Fatal(err)
	}

	prices := price.NewService(database, price.DefaultRegistry())

	t.Cleanup(func() { _ = prices.Close() })

	holdings := NewHoldingService(database, prices)
	for _, currency := range []string{"TRY", "USD", "EUR"} {
		data, getErr := holdings.GetDashboardData(t.Context(), currency)
		if getErr != nil {
			t.Fatal(getErr)
		}

		if !data.ValuationComplete || !data.EURAvailable || data.EURTRYRate != 50 ||
			data.CurrentValueTRY != 2400 || data.CurrentValueUSD != 60 || data.CurrentValueEUR != 48 ||
			data.EstimatedValueTRY != 4640 || data.EstimatedValueUSD != 116 || data.EstimatedValueEUR != 92.8 {
			t.Fatalf("%s totals: %+v", currency, data)
		}

		want := map[string]float64{"TRY": 2400, "USD": 60, "EUR": 48}[currency]
		if data.AllocationByType["Cash"] != want || data.AllocationByBasket["Savings"] != want ||
			data.AllocationByLoc["Bank"] != want {
			t.Fatalf("%s allocation: %+v", currency, data)
		}
	}

	snapshots := NewSnapshotService(database, prices, holdings)

	err = snapshots.CreateSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	saved, err := snapshots.GetSnapshots(t.Context(), 10, 0, 0)
	if err != nil || len(saved) != 1 {
		t.Fatalf("snapshots: %v %v", saved, err)
	}

	if saved[0].TotalValueEUR == nil || *saved[0].TotalValueEUR != 48 ||
		saved[0].EURTRYRate == nil ||
		*saved[0].EURTRYRate != 50 {
		t.Fatalf("EUR snapshot: %+v", saved[0])
	}

	points, err := snapshots.GetSnapshotsForChart(t.Context(), 1)
	if err != nil || len(points) != 1 || points[0].ValueEUR == nil || *points[0].ValueEUR != 48 {
		t.Fatalf("chart: %v %v", points, err)
	}

	history, err := snapshots.GetHoldingHistory(t.Context(), 3, 1)
	if err != nil || len(history) != 1 || history[0].ValueEUR == nil || *history[0].ValueEUR != 20 {
		t.Fatalf("holding history: %v %v", history, err)
	}

	_, err = database.ExecContext(
		t.Context(),
		`INSERT INTO snapshots(total_value_try,total_value_usd,usd_try_rate,holdings_json)
VALUES(100,2.5,40,'[{"id":3,"value_try":100,"value_usd":2.5,"valuation_available":true}]')`,
	)
	if err != nil {
		t.Fatal(err)
	}

	points, err = snapshots.GetSnapshotsForChart(t.Context(), 1)
	if err != nil || len(points) != 2 || points[1].ValueEUR != nil {
		t.Fatalf("legacy chart must have EUR gap: %v %v", points, err)
	}

	history, err = snapshots.GetHoldingHistory(t.Context(), 3, 1)
	if err != nil || len(history) != 2 || history[1].ValueEUR != nil {
		t.Fatalf("legacy holding must have EUR gap: %v %v", history, err)
	}
}

var errTestEUROffline = errors.New("test FX offline")

type unavailableEURGetter struct{}

func (unavailableEURGetter) Name() string       { return "Frankfurter" }
func (unavailableEURGetter) Type() string       { return "FX" }
func (unavailableEURGetter) TTL() time.Duration { return time.Hour }
func (unavailableEURGetter) GetPrice(context.Context, string) (models.Price, error) {
	return models.Price{}, errTestEUROffline
}

//nolint:gocyclo // Exercise independent optional, required, and estimate FX failure contracts.
func TestEURUnavailablePreservesTRYUSD(t *testing.T) {
	t.Parallel()
	database := valuationDatabase(t)

	_, err := database.ExecContext(t.Context(), `DELETE FROM price_cache WHERE instrument_id='EUR';
INSERT INTO holdings(name,asset_type,quantity,price_provider,base_currency,manual_price)
VALUES('Cash','Cash',2,'Manual','USD',10)`)
	if err != nil {
		t.Fatal(err)
	}

	registry := price.NewRegistry()
	registry.Register(unavailableEURGetter{})
	prices := price.NewService(database, registry)

	t.Cleanup(func() { _ = prices.Close() })

	holdings := NewHoldingService(database, prices)

	data, err := holdings.GetDashboardData(t.Context(), "TRY")
	if err != nil || !data.ValuationComplete || data.CurrentValueTRY != 800 || data.EURAvailable ||
		data.FXIsFailed {
		t.Fatalf("TRY dashboard: %+v %v", data, err)
	}

	_, err = holdings.GetDashboardData(t.Context(), "EUR")
	if err == nil {
		t.Fatal("EUR display succeeded without EUR FX")
	}

	snapshots := NewSnapshotService(database, prices, holdings)

	err = snapshots.CreateSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	saved, err := snapshots.GetSnapshots(t.Context(), 10, 0, 0)
	if err != nil || len(saved) != 1 || saved[0].TotalValueEUR != nil ||
		saved[0].EURTRYRate != nil ||
		saved[0].FailedCount != 0 {
		t.Fatalf("optional EUR outage: %+v %v", saved, err)
	}

	_, err = database.ExecContext(t.Context(), `UPDATE holdings SET base_currency='EUR'`)
	if err != nil {
		t.Fatal(err)
	}

	rows, err := holdings.GetWithPrices(t.Context(), "TRY")
	if err != nil || len(rows) != 1 || rows[0].ValuationAvailable || !rows[0].IsFailed ||
		rows[0].NormTotalTRY != 0 {
		t.Fatalf("EUR holding without FX: %+v %v", rows, err)
	}

	err = snapshots.CreateSnapshot(t.Context())
	if err == nil {
		t.Fatal("incomplete EUR snapshot succeeded")
	}

	_, err = database.ExecContext(t.Context(), `UPDATE holdings SET est_price=20`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = holdings.GetDashboardData(t.Context(), "TRY")
	if err == nil {
		t.Fatal("EUR estimate fabricated without FX")
	}
}

func TestEURProviderQuoteUsesQuoteCurrency(t *testing.T) {
	t.Parallel()
	database := valuationDatabase(t)

	_, err := database.ExecContext(
		t.Context(),
		`INSERT INTO holdings(name,asset_type,quantity,price_provider,instrument_id,base_currency,est_price)
VALUES('European stock','Stock',2,'Yahoo Finance','EURTEST','USD',100);
INSERT INTO price_cache(provider,instrument_id,price,currency,source,expires_at,last_error)
VALUES('Yahoo Finance','EURTEST',10,'EUR','test',?,'')`,
		time.Now().Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}

	prices := price.NewService(database, price.DefaultRegistry())

	t.Cleanup(func() { _ = prices.Close() })

	rows, err := NewHoldingService(database, prices).GetWithPrices(t.Context(), "EUR")
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows: %v %v", rows, err)
	}

	row := rows[0]
	if !row.ValuationAvailable || row.NormTotalTRY != 1000 || row.NormTotalUSD != 25 ||
		row.NormTotalEUR != 20 ||
		row.NormEstTotalEUR != 160 {
		t.Fatalf("quote/base conversion: %+v", row)
	}
}

//nolint:gocyclo // Snapshot gaps and estimate quality must remain consistent across currencies.
func TestStaleEURSnapshotIsGapAndEstimateIsFlagged(t *testing.T) {
	t.Parallel()
	database := valuationDatabase(t)
	old := time.Now().UTC().Add(-2 * time.Hour)

	_, err := database.ExecContext(
		t.Context(),
		`UPDATE price_cache SET fetched_at=?, expires_at=? WHERE instrument_id='EUR';
INSERT INTO holdings(name,asset_type,quantity,price_provider,base_currency,manual_price)
VALUES('Cash','Cash',2,'Manual','USD',10)`,
		old,
		old,
	)
	if err != nil {
		t.Fatal(err)
	}

	registry := price.NewRegistry()
	registry.Register(unavailableEURGetter{})
	prices := price.NewService(database, registry)

	t.Cleanup(func() { _ = prices.Close() })

	holdings := NewHoldingService(database, prices)
	snapshots := NewSnapshotService(database, prices, holdings)

	err = snapshots.CreateSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	saved, err := snapshots.GetSnapshots(t.Context(), 10, 0, 0)
	if err != nil || len(saved) != 1 || saved[0].TotalValueEUR != nil ||
		saved[0].EURTRYRate != nil ||
		saved[0].FailedCount != 0 ||
		saved[0].StaleCount != 0 {
		t.Fatalf(
			"stale optional EUR must leave gap without changing USD quality: %+v %v",
			saved,
			err,
		)
	}
	// Current quote is USD; only the estimate needs EUR. That dependency must warn.
	_, err = database.ExecContext(
		t.Context(),
		`UPDATE holdings SET price_provider='Manual', base_currency='EUR', manual_price=0,
est_price=20, instrument_id='QUOTE';
INSERT INTO price_cache(provider,instrument_id,price,currency,source,expires_at,last_error)
VALUES('Frankfurter','QUOTE',10,'USD','test',?,'');
UPDATE holdings SET price_provider='Frankfurter'`,
		time.Now().Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}

	data, err := holdings.GetDashboardData(t.Context(), "USD")
	if err != nil || !data.FXIsStale || !data.FXIsFailed || data.StaleCount != 1 ||
		data.FailedCount != 1 ||
		data.EstimatedValueUSD != 50 ||
		!data.FXFetchedAt.Equal(old) {
		t.Fatalf("EUR estimate dependency quality: %+v %v", data, err)
	}
}

type delayedEURGetter struct{ unavailableEURGetter }

func (delayedEURGetter) GetPrice(ctx context.Context, _ string) (models.Price, error) {
	const delay = 2100 * time.Millisecond

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return models.Price{}, errTestEUROffline
	case <-ctx.Done():
		return models.Price{}, wrapOperationError("delayed EUR", ctx.Err())
	}
}

func TestRequiredEURAllowsDelayedPrimaryFallback(t *testing.T) {
	t.Parallel()
	database := valuationDatabase(t)

	_, err := database.ExecContext(t.Context(), `DELETE FROM price_cache WHERE instrument_id='EUR';
INSERT INTO price_cache(provider,instrument_id,price,currency,source,expires_at,last_error)
VALUES('Yahoo Finance','EURTRY=X',50,'TRY','test',?,'');
INSERT INTO holdings(name,asset_type,quantity,base_currency,manual_price)
VALUES('Cash','Cash',2,'USD',10)`, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	registry := price.DefaultRegistry()
	registry.Register(delayedEURGetter{})
	prices := price.NewService(database, registry)

	t.Cleanup(func() { _ = prices.Close() })

	rows, err := NewHoldingService(database, prices).GetWithPrices(t.Context(), "EUR")
	if err != nil || len(rows) != 1 || rows[0].NormTotalEUR != 16 || !rows[0].ValuationAvailable {
		t.Fatalf(
			"required EUR should reach healthy fallback after optional budget: %+v %v",
			rows,
			err,
		)
	}
}

func TestZeroEURPositionsDoNotRequireFX(t *testing.T) {
	t.Parallel()

	positions := []models.HoldingWithPrice{{
		BaseCurrency: "EUR", CurrentCurrency: "EUR", EstPrice: 100, ValuationAvailable: true,
	}}
	if requiresEURValuation(positions, "TRY") || holdingNeedsEUR(&positions[0]) {
		t.Fatal("zero position made optional EUR rate required")
	}

	if !requiresEURValuation(positions, "EUR") {
		t.Fatal("EUR display must still require an exchange rate")
	}

	database := valuationDatabase(t)

	_, err := database.ExecContext(t.Context(), `DELETE FROM price_cache WHERE instrument_id='EUR';
INSERT INTO holdings(name,asset_type,quantity,base_currency,manual_price,est_price)
VALUES('Closed euro','Cash',0,'EUR',10,20)`)
	if err != nil {
		t.Fatal(err)
	}

	registry := price.NewRegistry()
	registry.Register(unavailableEURGetter{})
	prices := price.NewService(database, registry)

	t.Cleanup(func() { _ = prices.Close() })

	holdings := NewHoldingService(database, prices)

	err = NewSnapshotService(database, prices, holdings).CreateSnapshot(t.Context())
	if err != nil {
		t.Fatalf("zero EUR must retain known zero contribution without EUR FX: %v", err)
	}
}
