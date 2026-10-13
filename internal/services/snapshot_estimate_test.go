package services

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/price"
)

type snapshotEstimateQuoteGetter struct{ pendingEURGetter }

func (snapshotEstimateQuoteGetter) Name() string { return "Snapshot quote" }

func snapshotEstimateServices(
	t *testing.T,
	currency, eurState string,
	estimate float64,
) (*HoldingService, *SnapshotService) {
	t.Helper()
	database := valuationDatabase(t)

	_, err := database.ExecContext(t.Context(), `INSERT INTO holdings
(name,asset_type,quantity,price_provider,instrument_id,base_currency,est_price)
VALUES('Current quote with EUR target','Stock',2,'Snapshot quote','CURRENT','EUR',?)`, estimate)
	if err != nil {
		t.Fatal(err)
	}

	_, err = database.ExecContext(t.Context(), `INSERT INTO price_cache
(provider,instrument_id,price,currency,source,expires_at,last_error)
VALUES('Snapshot quote','CURRENT',10,?,'test',?,'')`, currency, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	registry := price.NewRegistry()
	registry.Register(snapshotEstimateQuoteGetter{})
	registry.Register(pendingEURGetter{})

	switch eurState {
	case "missing":
		registry.Register(unavailableEURGetter{})

		_, err = database.ExecContext(
			t.Context(),
			"DELETE FROM price_cache WHERE instrument_id='EUR'",
		)
	case "stale", "failed":
		errorCount, lastError := 0, ""
		if eurState == "failed" {
			errorCount, lastError = 1, errTestEUROffline.Error()
		}

		_, err = database.ExecContext(t.Context(), `UPDATE price_cache
SET expires_at=?, error_count=?, last_error=? WHERE instrument_id='EUR'`,
			time.Now().Add(-time.Hour), errorCount, lastError)
	}

	if err != nil {
		t.Fatal(err)
	}

	prices := price.NewService(database, registry)

	t.Cleanup(func() { _ = prices.Close() })

	holdings := NewHoldingService(database, prices)

	return holdings, NewSnapshotService(database, prices, holdings)
}

func TestSnapshotSeparatesEstimateEURDependencies(t *testing.T) {
	t.Parallel()

	for _, currency := range []string{"TRY", "USD", "EUR"} {
		for _, eurState := range []string{"fresh", "missing", "stale", "failed"} {
			for _, estimate := range []float64{0, 20} {
				name := currency + "/" + eurState + "/unset target"
				if estimate > 0 {
					name = currency + "/" + eurState + "/EUR target"
				}

				t.Run(name, func(t *testing.T) {
					t.Parallel()
					checkSnapshotEstimateEURDependency(t, currency, eurState, estimate)
				})
			}
		}
	}
}

//nolint:gocyclo // Compare current snapshots and estimate-aware dashboard contracts for each FX state.
func checkSnapshotEstimateEURDependency(t *testing.T, currency, eurState string, estimate float64) {
	t.Helper()
	holdings, snapshots := snapshotEstimateServices(t, currency, eurState, estimate)

	err := snapshots.CreateSnapshot(t.Context())

	if currency == "EUR" && eurState == "missing" {
		var incomplete *IncompleteValuationError
		if !errors.As(err, &incomplete) || incomplete.UnvaluedCount != 1 {
			t.Fatalf("missing current EUR conversion must reject snapshot: %v", err)
		}

		saved, getErr := snapshots.GetSnapshots(t.Context(), 10, 0, 0)
		if getErr != nil || len(saved) != 0 {
			t.Fatalf("incomplete current snapshot persisted: %+v, %v", saved, getErr)
		}

		return
	}

	if err != nil {
		t.Fatalf("complete current valuation rejected by unrelated target estimate: %v", err)
	}

	wantTRY := map[string]float64{"TRY": 20, "USD": 800, "EUR": 1000}[currency]
	wantStale := currency == "EUR" && (eurState == "stale" || eurState == "failed")
	wantFailed := currency == "EUR" && eurState == "failed"
	checkSnapshotEstimateHistory(t, snapshots, wantTRY, eurState == "fresh", wantStale, wantFailed)

	stored, err := holdings.GetByID(t.Context(), 1)
	if err != nil || stored == nil || stored.EstPrice != estimate {
		t.Fatalf("snapshot changed stored estimate: %+v, %v", stored, err)
	}

	data, err := holdings.GetDashboardData(t.Context(), "USD")
	if eurState == "missing" && estimate > 0 {
		if err == nil || data != nil {
			t.Fatalf("dashboard must fail closed for unavailable target FX: %+v, %v", data, err)
		}

		return
	}

	if err != nil {
		t.Fatal(err)
	}

	wantEstimate := wantTRY
	if estimate > 0 {
		wantEstimate = 2000
	}

	usesEUR := currency == "EUR" || estimate > 0
	if data.CurrentValueTRY != wantTRY || data.EstimatedValueTRY != wantEstimate ||
		data.FXIsStale != (usesEUR && (eurState == "stale" || eurState == "failed")) ||
		data.FXIsFailed != (usesEUR && eurState == "failed") {
		t.Fatalf("dashboard target valuation or FX quality changed: %+v", data)
	}
}

//nolint:gocyclo // Check aggregate and per-holding history, including optional EUR gaps and quote quality.
func checkSnapshotEstimateHistory(
	t *testing.T,
	snapshots *SnapshotService,
	wantTRY float64,
	wantEUR, wantStale, wantFailed bool,
) {
	t.Helper()

	saved, err := snapshots.GetSnapshots(t.Context(), 10, 0, 0)
	if err != nil || len(saved) != 1 {
		t.Fatalf("snapshots: %+v, %v", saved, err)
	}

	snapshot := saved[0]
	if snapshot.TotalValueTRY != wantTRY || snapshot.TotalValueUSD != wantTRY/40 ||
		snapshot.USDTRYRate != 40 || snapshot.StaleCount != boolCount(wantStale) ||
		snapshot.FailedCount != boolCount(wantFailed) {
		t.Fatalf("current snapshot totals or quote quality changed: %+v", snapshot)
	}

	if wantEUR {
		if snapshot.TotalValueEUR == nil || *snapshot.TotalValueEUR != wantTRY/50 ||
			snapshot.EURTRYRate == nil || *snapshot.EURTRYRate != 50 {
			t.Fatalf("fresh historical EUR missing: %+v", snapshot)
		}
	} else if snapshot.TotalValueEUR != nil || snapshot.EURTRYRate != nil {
		t.Fatalf("unavailable or stale historical EUR must remain null: %+v", snapshot)
	}

	history, err := snapshots.GetHoldingHistory(t.Context(), 1, 1)
	if err != nil || len(history) != 1 {
		t.Fatalf("holding history: %+v, %v", history, err)
	}

	point := history[0]
	if !point.ValuationAvailable || point.ValueTRY != wantTRY || point.ValueUSD != wantTRY/40 ||
		point.IsStale != wantStale || point.IsFailed != wantFailed ||
		(point.ValueEUR != nil) != wantEUR {
		t.Fatalf("current holding history changed: %+v", point)
	}

	if point.ValueEUR != nil && *point.ValueEUR != wantTRY/50 {
		t.Fatalf("wrong holding EUR history: %+v", point)
	}
}

func boolCount(value bool) int {
	if value {
		return 1
	}

	return 0
}

func TestSnapshotIgnoresTargetEstimateOverflow(t *testing.T) {
	t.Parallel()

	for _, currency := range []string{"TRY", "USD", "EUR"} {
		for _, source := range []string{"manual", "automatic"} {
			t.Run(currency+"/"+source, func(t *testing.T) {
				t.Parallel()
				holdings, snapshots := snapshotEstimateServices(t, "USD", "fresh", math.MaxFloat64)

				_, err := holdings.db.ExecContext(
					t.Context(),
					"UPDATE holdings SET base_currency=?",
					currency,
				)
				if err != nil {
					t.Fatal(err)
				}

				wantTRY := float64(800)

				if source == "manual" {
					_, err = holdings.db.ExecContext(
						t.Context(),
						"UPDATE holdings SET manual_price=10",
					)
					if err != nil {
						t.Fatal(err)
					}

					wantTRY = map[string]float64{"TRY": 20, "USD": 800, "EUR": 1000}[currency]
				}

				err = snapshots.CreateSnapshot(t.Context())
				if err != nil {
					t.Fatalf("target overflow blocked finite current snapshot: %v", err)
				}

				checkSnapshotEstimateHistory(t, snapshots, wantTRY, true, false, false)

				_, err = holdings.GetDashboardData(t.Context(), "USD")
				if !errors.Is(err, errNonFiniteHoldingValue) {
					t.Fatalf("dashboard must still reject overflowing target estimate: %v", err)
				}
			})
		}
	}
}

func TestSnapshotEstimatesCannotReplaceMissingCurrentValue(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		query string
	}{
		{name: "missing current quote", query: "DELETE FROM price_cache WHERE provider='Snapshot quote'"},
		{
			name:  "unsupported current currency",
			query: "UPDATE price_cache SET currency='GBP' WHERE provider='Snapshot quote'",
		},
		{name: "manual EUR current requires FX", query: "UPDATE holdings SET manual_price=10"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			holdings, snapshots := snapshotEstimateServices(t, "USD", "missing", 20)
			// Remove automatic quote requests without a cache instead of waiting for their timeout.
			if test.name == "missing current quote" {
				_, err := holdings.db.ExecContext(
					t.Context(),
					"UPDATE holdings SET price_provider='Manual'",
				)
				if err != nil {
					t.Fatal(err)
				}
			}

			_, err := holdings.db.ExecContext(t.Context(), test.query)
			if err != nil {
				t.Fatal(err)
			}

			err = snapshots.CreateSnapshot(t.Context())

			var incomplete *IncompleteValuationError
			if !errors.As(err, &incomplete) || incomplete.UnvaluedCount != 1 {
				t.Fatalf("target cannot replace missing current valuation: %v", err)
			}

			saved, err := snapshots.GetSnapshots(t.Context(), 10, 0, 0)
			if err != nil || len(saved) != 0 {
				t.Fatalf("incomplete current snapshot persisted: %+v, %v", saved, err)
			}
		})
	}
}

func TestSnapshotEstimatesPreserveCurrentQuoteQuality(t *testing.T) {
	t.Parallel()

	for _, instrument := range []string{"CURRENT", "USD"} {
		for _, errorCount := range []int{0, 1} {
			name := instrument + "/stale"
			if errorCount > 0 {
				name = instrument + "/failed"
			}

			t.Run(name, func(t *testing.T) {
				t.Parallel()
				holdings, snapshots := snapshotEstimateServices(t, "USD", "fresh", 20)

				_, err := holdings.db.ExecContext(t.Context(), `UPDATE price_cache
SET expires_at=?, error_count=?, last_error='refresh pending' WHERE instrument_id=?`,
					time.Now().Add(-time.Hour), errorCount, instrument)
				if err != nil {
					t.Fatal(err)
				}

				err = snapshots.CreateSnapshot(t.Context())
				if err != nil {
					t.Fatal(err)
				}

				checkSnapshotEstimateHistory(t, snapshots, 800, true, true, errorCount > 0)
			})
		}
	}
}

func TestSnapshotRejectsCurrentOverflowWithEstimate(t *testing.T) {
	t.Parallel()
	holdings, snapshots := snapshotEstimateServices(t, "USD", "fresh", 20)

	_, err := holdings.db.ExecContext(
		t.Context(),
		"UPDATE price_cache SET price=? WHERE instrument_id='CURRENT'",
		math.MaxFloat64,
	)
	if err != nil {
		t.Fatal(err)
	}

	err = snapshots.CreateSnapshot(t.Context())
	if !errors.Is(err, errNonFiniteHoldingValue) {
		t.Fatalf("current overflow must still reject snapshot: %v", err)
	}

	saved, err := snapshots.GetSnapshots(t.Context(), 10, 0, 0)
	if err != nil || len(saved) != 0 {
		t.Fatalf("non-finite current snapshot persisted: %+v, %v", saved, err)
	}
}
