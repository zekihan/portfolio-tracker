package services

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestSettingsRespectCancellation(t *testing.T) {
	t.Parallel()

	for _, operation := range []string{"read", "currency", "mode", "interval"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			service := NewSettingsService(database)

			before, err := service.GetAll(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			switch operation {
			case "read":
				_, err = service.GetAll(ctx)
			case "currency":
				err = service.SetDisplayCurrency(ctx, "USD")
			case "mode":
				err = service.SetDarkMode(ctx, "dark")
			case "interval":
				err = service.SetRefreshInterval(ctx, 120)
			}

			if !errors.Is(err, context.Canceled) {
				t.Errorf("error = %v, want cancellation", err)
			}

			after, err := service.GetAll(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			if after != before {
				t.Errorf("canceled operation changed settings: %+v -> %+v", before, after)
			}
		})
	}
}
