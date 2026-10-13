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

type dashboardFXGetter struct{ entered chan struct{} }

func (g *dashboardFXGetter) Name() string       { return "Frankfurter" }
func (g *dashboardFXGetter) Type() string       { return "FX" }
func (g *dashboardFXGetter) TTL() time.Duration { return time.Hour }
func (g *dashboardFXGetter) GetPrice(ctx context.Context, _ string) (models.Price, error) {
	close(g.entered)
	<-ctx.Done()

	return models.Price{}, wrapOperationError("test FX", ctx.Err())
}

func TestDashboardPropagatesFXCancellation(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	getter := &dashboardFXGetter{entered: make(chan struct{})}
	registry := price.NewRegistry()
	registry.Register(getter)
	prices := price.NewService(database, registry)

	t.Cleanup(func() { _ = prices.Close() })

	service := NewHoldingService(database, prices)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	type result struct {
		dashboard *models.DashboardData
		err       error
	}

	done := make(chan result, 1)

	go func() {
		dashboard, getErr := service.GetDashboardData(ctx, "TRY")
		done <- result{dashboard: dashboard, err: getErr}
	}()

	select {
	case <-getter.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("FX fetch did not start")
	}

	cancel()

	select {
	case got := <-done:
		if !errors.Is(got.err, context.Canceled) || got.dashboard != nil {
			t.Fatalf("got %+v, %v; want nil dashboard and cancellation", got.dashboard, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dashboard ignored cancellation")
	}
}

func TestDashboardCachedFXHonorsCanceledContext(t *testing.T) {
	t.Parallel()
	database := valuationDatabase(t)
	prices := price.NewService(database, price.DefaultRegistry())

	t.Cleanup(func() { _ = prices.Close() })

	service := NewHoldingService(database, prices)

	dashboard, err := service.GetDashboardData(t.Context(), "TRY")
	if err != nil || dashboard == nil || dashboard.USDTRYRate != 40 ||
		!dashboard.ValuationComplete {
		t.Fatalf("invalid complete dashboard: %+v, %v", dashboard, err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	dashboard, err = service.GetDashboardData(ctx, "TRY")
	if !errors.Is(err, context.Canceled) || dashboard != nil {
		t.Fatalf("got %+v, %v; want nil dashboard and cancellation", dashboard, err)
	}
}
