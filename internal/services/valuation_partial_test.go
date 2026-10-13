package services

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

func valuationDatabase(t *testing.T) *sqlx.DB {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.ExecContext(t.Context(), `INSERT INTO price_cache
 (provider,instrument_id,price,currency,source,expires_at,last_error)
 VALUES ('Frankfurter','USD',40,'TRY','test',?,'')`, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	return database
}

//nolint:gocyclo // End-to-end mixed portfolio contract covers per-row and aggregate invariants.
func TestPartialValuationIsExplicitAndCannotBecomeSnapshot(t *testing.T) {
	t.Parallel()
	database := valuationDatabase(t)

	_, err := database.ExecContext(t.Context(), `INSERT INTO holdings
 (name,asset_type,quantity,price_provider,instrument_id,base_currency,manual_price,est_price,
 basket,location,notes,ticker) VALUES
 ('Manual','Cash',2,'Manual','','TRY',100,200,'','','',''),
 ('USD','Stock',1,'Cached','USD','USD',0,0,'','','',''),
 ('EUR','Stock',1,'Cached','EUR','TRY',0,0,'','','',''),
 ('Missing','Stock',1,'Manual','','TRY',0,50,'','','',''),
 ('Stale','Stock',1,'Cached','STALE','TRY',0,0,'','','','')`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = database.ExecContext(t.Context(), `INSERT INTO price_cache
 (provider,instrument_id,price,currency,source,expires_at,last_error) VALUES
 ('Cached','USD',100,'USD','test',?,''),
 ('Cached','EUR',100,'EUR','test',?,''),
 ('Cached','STALE',120,'TRY','test',?,'')`,
		time.Now().Add(time.Hour), time.Now().Add(time.Hour), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	registry := price.DefaultRegistry()
	registry.Register(
		&boundedValuationGetter{
			active:  new(atomic.Int32),
			maximum: new(atomic.Int32),
			entered: make(chan struct{}, 10),
			release: make(chan struct{}),
			name:    "Cached",
		},
	)
	prices := price.NewService(database, registry)

	t.Cleanup(func() { _ = prices.Close() })

	service := NewHoldingService(database, prices)

	holdings, err := service.GetWithPrices(t.Context(), "TRY")
	if err != nil {
		t.Fatal(err)
	}

	if len(holdings) != 5 {
		t.Fatalf("got %d rows", len(holdings))
	}

	for _, holding := range holdings {
		available := holding.Name != "EUR" && holding.Name != "Missing"
		if holding.ValuationAvailable != available {
			t.Fatalf("wrong availability: %+v", holding)
		}

		if !available &&
			(!holding.IsFailed || holding.PriceError == "" || holding.NormTotalTRY != 0 || holding.NormTotalUSD != 0) {
			t.Fatalf("misleading unavailable row: %+v", holding)
		}

		if holding.Name == "Stale" && !holding.IsStale {
			t.Fatal("stale cache not labeled")
		}
	}

	data, err := service.GetDashboardData(t.Context(), "USD")
	if err != nil {
		t.Fatal(err)
	}

	if data.ValuationComplete || data.ValuedCount != 3 || data.UnvaluedCount != 2 ||
		data.FailedCount != 2 ||
		data.StaleCount != 1 ||
		data.TotalHoldings != 5 {
		t.Fatalf("wrong partial status: %+v", data)
	}

	if data.CurrentValueTRY != 4320 || data.CurrentValueUSD != 108 ||
		data.EstimatedValueTRY != 450 {
		t.Fatalf("wrong partial totals: %+v", data)
	}

	snapshots := NewSnapshotService(database, prices, service)
	err = snapshots.CreateSnapshot(t.Context())

	var incomplete *IncompleteValuationError
	if !errors.As(err, &incomplete) || incomplete.UnvaluedCount != 2 ||
		incomplete.TotalHoldings != 5 {
		t.Fatalf("expected typed incomplete error, got %v", err)
	}

	var count int

	err = database.GetContext(
		t.Context(),
		&count,
		"SELECT COUNT(*) FROM snapshots",
	)
	if err != nil {
		t.Fatal(err)
	}

	if count != 0 {
		t.Fatal("partial snapshot persisted")
	}
}

type boundedValuationGetter struct {
	active  *atomic.Int32
	maximum *atomic.Int32
	entered chan struct{}
	release <-chan struct{}
	name    string
}

func (getter *boundedValuationGetter) Name() string       { return getter.name }
func (getter *boundedValuationGetter) Type() string       { return "Stock" }
func (getter *boundedValuationGetter) TTL() time.Duration { return time.Hour }

func (getter *boundedValuationGetter) GetPrice(
	ctx context.Context,
	_ string,
) (models.Price, error) {
	active := getter.active.Add(1)
	defer getter.active.Add(-1)

	for prior := getter.maximum.Load(); active > prior; prior = getter.maximum.Load() {
		if getter.maximum.CompareAndSwap(prior, active) {
			break
		}
	}

	getter.entered <- struct{}{}

	select {
	case <-getter.release:
		return models.Price{Value: 10, Currency: "TRY", Source: "test", FetchedAt: time.Now()}, nil
	case <-ctx.Done():
		return models.Price{}, wrapOperationError("test quote", ctx.Err())
	}
}

func boundedValuationService(
	t *testing.T,
	count int,
	release <-chan struct{},
) (*HoldingService, *atomic.Int32, chan struct{}) {
	t.Helper()
	database := valuationDatabase(t)
	registry := price.DefaultRegistry()
	active, maximum := new(atomic.Int32), new(atomic.Int32)

	entered := make(chan struct{}, count)
	for index := range count {
		name := fmt.Sprintf("Provider%d", index)
		registry.Register(
			&boundedValuationGetter{
				active:  active,
				maximum: maximum,
				entered: entered,
				release: release,
				name:    name,
			},
		)

		_, err := database.ExecContext(t.Context(), `INSERT INTO holdings
 (name,asset_type,quantity,price_provider,instrument_id,base_currency,basket,location,notes,ticker,est_price)
 VALUES (?,'Stock',1,?,'TEST','TRY','','','','',0)`, name, name)
		if err != nil {
			t.Fatal(err)
		}
	}

	prices := price.NewService(database, registry)

	t.Cleanup(func() { _ = prices.Close() })

	return NewHoldingService(database, prices), maximum, entered
}

func TestValuationWorkersAreBoundedAndPreserveOrder(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	service, maximum, entered := boundedValuationService(t, 12, release)

	expected, err := service.GetAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		err      error
		holdings []models.HoldingWithPrice
	}

	done := make(chan result, 1)

	go func() {
		holdings, getErr := service.GetWithPrices(t.Context(), "TRY")
		done <- result{holdings: holdings, err: getErr}
	}()

	for range 4 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatal("four independent providers did not start concurrently")
		}
	}

	close(release)

	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}

	if maximum.Load() != 4 {
		t.Fatalf("peak concurrency=%d, want 4", maximum.Load())
	}

	if len(got.holdings) != len(expected) {
		t.Fatal("lost holdings")
	}

	for index, holding := range got.holdings {
		if holding.ID != expected[index].ID || !holding.ValuationAvailable {
			t.Fatalf("wrong ordered result at %d: %+v", index, holding)
		}
	}
}

func TestValuationBudgetReturnsUnavailableRows(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	service, _, _ := boundedValuationService(t, 12, release)
	started := time.Now()
	holdings, err := service.getWithPrices(t.Context(), "TRY", 100*time.Millisecond)

	close(release)

	if err != nil {
		t.Fatal(err)
	}

	if time.Since(started) > 2*time.Second {
		t.Fatal("valuation exceeded shared budget")
	}

	if len(holdings) != 12 {
		t.Fatalf("got %d rows", len(holdings))
	}

	for _, holding := range holdings {
		if holding.ValuationAvailable || !holding.IsFailed ||
			holding.PriceError == "" ||
			holding.Name == "" {
			t.Fatalf("missing unavailable metadata: %+v", holding)
		}
	}
}

//nolint:gocyclo // Checks one cached FX sample through dashboard, snapshot and history end to end.
func TestStaleFXQualityAndSnapshotRateStayConsistent(t *testing.T) {
	t.Parallel()
	database := valuationDatabase(t)

	_, err := database.ExecContext(
		t.Context(),
		`UPDATE price_cache SET expires_at=?, error_count=1, last_error='private upstream detail'
 WHERE provider='Frankfurter'`,
		time.Now().Add(-time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Keep both primary and fallback behavior isolated from real upstreams.
	registry := price.NewRegistry()
	registry.Register(price.NewManual())
	registry.Register(
		&boundedValuationGetter{
			active:  new(atomic.Int32),
			maximum: new(atomic.Int32),
			entered: make(chan struct{}, 10),
			release: make(chan struct{}),
			name:    "Frankfurter",
		},
	)
	prices := price.NewService(database, registry)

	t.Cleanup(func() { _ = prices.Close() })

	service := NewHoldingService(database, prices)
	holding := models.Holding{
		Name:          "Manual USD",
		AssetType:     models.AssetTypeCash,
		BaseCurrency:  "USD",
		Quantity:      2,
		PriceProvider: "Manual",
	}

	err = service.Create(t.Context(), &holding)
	if err != nil {
		t.Fatal(err)
	}

	err = service.UpdateManualPrice(t.Context(), holding.ID, 10)
	if err != nil {
		t.Fatal(err)
	}

	data, err := service.GetDashboardData(t.Context(), "TRY")
	if err != nil {
		t.Fatal(err)
	}

	if !data.ValuationComplete || !data.FXIsStale || !data.FXIsFailed || data.StaleCount != 1 ||
		data.FailedCount != 1 ||
		data.USDTRYRate != 40 ||
		data.CurrentValueTRY != 800 {
		t.Fatalf("incorrect stale FX quality: %+v", data)
	}

	snapshots := NewSnapshotService(database, prices, service)

	err = snapshots.CreateSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	saved, err := snapshots.GetSnapshots(t.Context(), 10, 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(saved) != 1 || saved[0].TotalValueTRY != saved[0].TotalValueUSD*saved[0].USDTRYRate ||
		saved[0].StaleCount != 1 {
		t.Fatalf("inconsistent snapshot: %+v", saved)
	}

	points, err := snapshots.GetHoldingHistory(t.Context(), holding.ID, 1)
	if err != nil {
		t.Fatal(err)
	}

	if len(points) != 1 || !points[0].ValuationAvailable || !points[0].IsStale ||
		!points[0].IsFailed {
		t.Fatalf("usable cached valuation lost: %+v", points)
	}
}
