package services

import (
	"path/filepath"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestHoldingHistoryUSDValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
		want float64
	}{
		{name: "missing", json: `[{"id":1,"value_try":400}]`, want: 0},
		{name: "null", json: `[{"id":1,"value_try":400,"value_usd":null}]`, want: 0},
		{name: "string", json: `[{"id":1,"value_try":400,"value_usd":"10"}]`, want: 0},
		{name: "number", json: `[{"id":1,"value_try":400,"value_usd":10}]`, want: 10},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			_, err = database.ExecContext(t.Context(), `
				INSERT INTO snapshots
				(total_value_try, total_value_usd, usd_try_rate, holdings_json)
				VALUES (400, 10, 40, ?)
			`, test.json)
			if err != nil {
				t.Fatal(err)
			}

			service := NewSnapshotService(database, nil, nil)

			points, err := service.GetHoldingHistory(t.Context(), 1, 1)
			if err != nil {
				t.Fatal(err)
			}

			if len(points) != 1 {
				t.Fatalf("got %d history points, want 1", len(points))
			}

			if points[0].ValueTRY != 400 || points[0].ValueUSD != test.want {
				t.Errorf("got TRY=%v USD=%v, want TRY=400 USD=%v",
					points[0].ValueTRY, points[0].ValueUSD, test.want)
			}
		})
	}
}
