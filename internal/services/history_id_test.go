package services

import (
	"path/filepath"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestHoldingHistoryMatchesExactID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
		id   int
	}{
		{name: "fractional", id: 1, json: `[
			{"id":1.5,"value_try":999,"value_usd":999},
			{"id":1,"value_try":400,"value_usd":10}]`},
		{name: "large integer", id: 9007199254740993, json: `[
			{"id":9007199254740992,"value_try":999,"value_usd":999},
			{"id":9007199254740993,"value_try":400,"value_usd":10}]`},
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

			points, err := service.GetHoldingHistory(t.Context(), test.id, 1)
			if err != nil {
				t.Fatal(err)
			}

			if len(points) != 1 {
				t.Fatalf("got %d points, want 1", len(points))
			}

			if points[0].ValueTRY != 400 || points[0].ValueUSD != 10 {
				t.Fatalf("history matched a different ID: %+v", points[0])
			}
		})
	}
}
