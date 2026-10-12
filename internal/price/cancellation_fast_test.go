package price

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
)

func TestCanceledPriceLookupsRejectFastPaths(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"fresh cache", "open breaker", "manual"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			service := NewService(database, DefaultRegistry())

			provider, instrument := "Yahoo Finance", "TEST"
			if mode == "manual" {
				provider, instrument = "Manual", "100"
			} else {
				ttl := time.Hour
				if mode == "open breaker" {
					ttl = -time.Hour
					service.breakers[provider] = true
				}

				err = service.cachePrice(provider, instrument, &models.Price{
					Value: 100, Currency: "USD", Source: provider, FetchedAt: time.Now().UTC(),
					Error: nil, IsStale: false, IsFailed: false,
				}, ttl)
				if err != nil {
					t.Fatal(err)
				}
			}

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			_, err = service.GetPrice(ctx, provider, instrument)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want cancellation", err)
			}

			if service.failures[provider] != 0 {
				t.Fatal("cancellation penalized provider health")
			}
		})
	}
}
