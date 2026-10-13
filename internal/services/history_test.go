package services

import (
	"path/filepath"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestHoldingHistoryUSDValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		json      string
		want      float64
		available bool
	}{
		{name: "missing", json: `[{"id":1,"value_try":400}]`, want: 0},
		{name: "null", json: `[{"id":1,"value_try":400,"value_usd":null}]`, want: 0},
		{name: "string", json: `[{"id":1,"value_try":400,"value_usd":"10"}]`, want: 0},
		{
			name:      "number",
			json:      `[{"id":1,"value_try":400,"value_usd":10}]`,
			want:      10,
			available: true,
		},
		{
			name: "legacy failed",
			json: `[{"id":1,"value_try":400,"value_usd":10,"is_failed":true}]`,
			want: 10,
		},
		{
			name:      "stale usable",
			json:      `[{"id":1,"value_try":400,"value_usd":10,"is_stale":true}]`,
			want:      10,
			available: true,
		},
		{
			name: "explicit unavailable",
			json: `[{"id":1,"value_try":400,"value_usd":10,"valuation_available":false}]`,
			want: 10,
		},
		{
			name:      "explicit cached failure",
			json:      `[{"id":1,"value_try":400,"value_usd":10,"is_failed":true,"valuation_available":true}]`,
			want:      10,
			available: true,
		},
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

			if points[0].ValuationAvailable != test.available {
				t.Fatalf("wrong availability: %+v", points[0])
			}

			if points[0].ValueTRY != 400 || points[0].ValueUSD != test.want {
				t.Errorf("got TRY=%v USD=%v, want TRY=400 USD=%v",
					points[0].ValueTRY, points[0].ValueUSD, test.want)
			}
		})
	}
}

func TestSnapshotDateFilterPrecedesPagination(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.ExecContext(t.Context(), `INSERT INTO snapshots
 (timestamp,total_value_try,total_value_usd,usd_try_rate,holdings_json)
 VALUES (datetime('now','-20 days'),400,10,40,'[]'),
 (datetime('now','-2 days'),800,20,40,'[]'),
 (datetime('now','-1 day'),1200,30,40,'[]')`)
	if err != nil {
		t.Fatal(err)
	}

	service := NewSnapshotService(database, nil, nil)

	snapshots, err := service.GetSnapshots(t.Context(), 1, 1, 7)
	if err != nil {
		t.Fatal(err)
	}

	if len(snapshots) != 1 || snapshots[0].TotalValueTRY != 800 {
		t.Fatalf("unexpected filtered page: %+v", snapshots)
	}

	snapshots, err = service.GetSnapshots(t.Context(), 1, 2, 7)
	if err != nil {
		t.Fatal(err)
	}

	if len(snapshots) != 0 {
		t.Fatalf("old snapshot appeared in date range: %+v", snapshots)
	}
}
