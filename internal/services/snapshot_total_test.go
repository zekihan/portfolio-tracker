package services

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

func TestSnapshotRejectsOverflowingTotals(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		value float64
	}{
		{name: "finite", value: 100},
		{name: "overflow", value: math.MaxFloat64 * 0.75},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := test.value

			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			_, err = database.ExecContext(t.Context(), `INSERT INTO price_cache
				(provider, instrument_id, price, currency, source, expires_at, last_error)
				VALUES ('Frankfurter', 'USD', 40, 'TRY', 'test', ?, '')`, time.Now().UTC().Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}

			_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
				(name, asset_type, quantity, base_currency, manual_price,
				ticker, basket, location, notes, price_provider, instrument_id, est_price) VALUES
				('A', 'Cash', 1, 'TRY', ?, '', '', '', '', 'Manual', '', 0),
				('B', 'Cash', 1, 'TRY', ?, '', '', '', '', 'Manual', '', 0)`, value, value)
			if err != nil {
				t.Fatal(err)
			}

			seedEURQuote(t, database)
			prices := price.NewService(database, price.DefaultRegistry())
			service := NewSnapshotService(database, prices, NewHoldingService(database, prices))
			err = service.CreateSnapshot(t.Context())

			wantCount := 1
			if test.name == "overflow" {
				wantCount = 0

				if err == nil {
					t.Error("accepted infinite aggregate total")
				}
			} else if err != nil {
				t.Fatal(err)
			}

			snapshots, err := service.GetSnapshots(t.Context(), 10, 0, 0)
			if err != nil {
				t.Fatal(err)
			}

			if len(snapshots) != wantCount {
				t.Fatalf("saved %d snapshots, want %d", len(snapshots), wantCount)
			}

			if wantCount == 1 &&
				(snapshots[0].TotalValueTRY != 200 || snapshots[0].TotalValueUSD != 5) {
				t.Errorf("unexpected snapshot totals: %+v", snapshots[0])
			}
		})
	}
}
