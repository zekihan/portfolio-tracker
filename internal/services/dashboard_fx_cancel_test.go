package services

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

type dashboardFXGetter struct {
	cancel context.CancelFunc
	calls  int
}

func (g *dashboardFXGetter) Name() string       { return "Frankfurter" }
func (g *dashboardFXGetter) Type() string       { return "FX" }
func (g *dashboardFXGetter) TTL() time.Duration { return -time.Hour }
func (g *dashboardFXGetter) GetPrice(context.Context, string) (models.Price, error) {
	g.calls++
	if g.calls == 2 && g.cancel != nil {
		g.cancel()
	}

	var quote models.Price

	quote.Value = 40
	quote.Currency = "TRY"
	quote.Source = "test"
	quote.FetchedAt = time.Now().UTC()

	return quote, nil
}

func TestDashboardPropagatesFinalFXCancellation(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		cancel bool
	}{
		{name: "live", cancel: false}, {name: "canceled final FX lookup", cancel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			getter := &dashboardFXGetter{cancel: nil, calls: 0}
			if test.cancel {
				getter.cancel = cancel
			}

			registry := price.NewRegistry()
			registry.Register(getter)
			service := NewHoldingService(database, price.NewService(database, registry))
			dashboard, err := service.GetDashboardData(ctx, "TRY")

			if getter.calls != 2 {
				t.Fatalf("FX lookup count = %d, want 2", getter.calls)
			}

			if test.cancel {
				if !errors.Is(err, context.Canceled) {
					t.Errorf("dashboard error = %v, want cancellation", err)
				}

				if dashboard != nil {
					t.Error("returned dashboard after FX lookup cancellation")
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if dashboard == nil || dashboard.USDTRYRate != 40 {
				t.Errorf("invalid live dashboard: %+v", dashboard)
			}
		})
	}
}
