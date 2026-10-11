package services

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestHoldingFiltersRespectCancellation(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
		(name, asset_type, basket, location) VALUES
		('A', 'Cash', 'Z', 'Z'), ('B', 'Cash', 'A', 'A'),
		('C', 'Cash', 'A', 'A'), ('D', 'Cash', '', '')`)
	if err != nil {
		t.Fatal(err)
	}

	service := NewHoldingService(database, nil)
	for _, test := range []struct {
		query func(context.Context) ([]string, error)
		name  string
	}{
		{name: "baskets", query: service.GetUniqueBaskets},
		{name: "locations", query: service.GetUniqueLocations},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			_, err := test.query(ctx)
			if !errors.Is(err, context.Canceled) {
				t.Errorf("error = %v, want cancellation", err)
			}

			values, err := test.query(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			if !slices.Equal(values, []string{"A", "Z"}) {
				t.Errorf("filter values = %v, want [A Z]", values)
			}
		})
	}
}
