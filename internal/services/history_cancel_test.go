package services

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestHistoryQueriesRespectCancellation(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.ExecContext(t.Context(), `
		INSERT INTO snapshots
		(total_value_try, total_value_usd, usd_try_rate, holdings_json)
		VALUES (400, 10, 40, '[{"id":1,"value_try":400,"value_usd":10}]')
	`)
	if err != nil {
		t.Fatal(err)
	}

	service := NewSnapshotService(database, nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	queries := []struct {
		run  func() error
		name string
	}{
		{name: "snapshots", run: func() error {
			_, queryErr := service.GetSnapshots(ctx, 10, 0)

			return queryErr
		}},
		{name: "chart", run: func() error {
			_, queryErr := service.GetSnapshotsForChart(ctx, 365)

			return queryErr
		}},
		{name: "holding", run: func() error {
			_, queryErr := service.GetHoldingHistory(ctx, 1, 365)

			return queryErr
		}},
	}

	for _, query := range queries {
		err = query.run()
		if !errors.Is(err, context.Canceled) {
			t.Errorf("%s: got %v, want context.Canceled", query.name, err)
		}
	}
}
