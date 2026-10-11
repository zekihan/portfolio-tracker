package price

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
)

type countingRefreshGetter struct {
	Getter

	calls atomic.Int64
}

func (g *countingRefreshGetter) Name() string { return "Counting refresh" }

func (g *countingRefreshGetter) GetPrice(
	ctx context.Context,
	instrument string,
) (models.Price, error) {
	g.calls.Add(1)

	quote, err := g.Getter.GetPrice(ctx, instrument)
	if err != nil {
		return quote, fmt.Errorf("counting getter: %w", err)
	}

	return quote, nil
}

func TestRefreshAllDeduplicatesFailedLookups(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	getter := &countingRefreshGetter{
		Getter: &unavailableGetter{err: errProviderUnavailable}, calls: atomic.Int64{},
	}
	registry := NewRegistry()
	registry.Register(getter)
	service := NewService(database, registry)

	var holding models.Holding

	holding.PriceProvider = getter.Name()
	holding.InstrumentID = "TEST"

	err = service.RefreshAll(t.Context(), []models.Holding{holding, holding, holding})
	if !errors.Is(err, errProviderUnavailable) {
		t.Fatalf("refresh lost provider failure: %v", err)
	}

	if calls := getter.calls.Load(); calls != 1 {
		t.Errorf("failed lookup fetched %d times, want 1", calls)
	}

	if service.failures[getter.Name()] != 1 || service.breakers[getter.Name()] {
		t.Error("duplicate holdings inflated failures or opened breaker")
	}
}

func TestRefreshAllDeduplicatesProviderInstruments(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	getter := &countingRefreshGetter{Getter: NewManual(), calls: atomic.Int64{}}
	registry := NewRegistry()
	registry.Register(getter)
	service := NewService(database, registry)

	var first models.Holding

	first.PriceProvider = getter.Name()
	first.InstrumentID = "100"
	second := first
	second.InstrumentID = "200"

	err = service.RefreshAll(t.Context(), []models.Holding{first, first, second, second})
	if err != nil {
		t.Fatal(err)
	}

	if calls := getter.calls.Load(); calls != 2 {
		t.Errorf("provider fetched %d times, want one fetch per distinct instrument (2)", calls)
	}

	for _, instrument := range []string{"100", "200"} {
		cached, cacheErr := service.getCachedPrice(getter.Name(), instrument)
		if cacheErr != nil {
			t.Fatal(cacheErr)
		}

		if cached == nil {
			t.Errorf("missing refreshed quote for %s", instrument)
		}
	}

	err = service.RefreshAll(t.Context(), []models.Holding{first, second})
	if err != nil {
		t.Fatal(err)
	}

	if calls := getter.calls.Load(); calls != 4 {
		t.Errorf("second refresh fetched %d total times, want 4", calls)
	}
}
