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

func TestFailedInstrumentNegativeCache(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	getter := &selectiveFailureGetter{failure: instrumentFailure(errInstrument)}
	registry := NewRegistry()
	registry.Register(getter)
	service := NewService(database, registry)

	t.Cleanup(func() { _ = service.Close() })

	for range 5 {
		_, err = service.GetPrice(t.Context(), getter.Name(), "BAD")
		if !errors.Is(err, errInstrument) {
			t.Fatalf("failure lost: %v", err)
		}
	}

	if getter.calls != 1 {
		t.Fatalf("negative cache made %d calls, want 1", getter.calls)
	}

	_, err = service.GetPrice(t.Context(), getter.Name(), "GOOD")
	if err != nil {
		t.Fatal(err)
	}

	if getter.calls != 2 {
		t.Fatalf("negative cache affected sibling: %d calls", getter.calls)
	}

	_, err = service.getPrice(t.Context(), getter.Name(), "BAD", true)
	if !errors.Is(err, errInstrument) || getter.calls != 3 {
		t.Fatalf("explicit retry failed: calls=%d err=%v", getter.calls, err)
	}

	key := refreshKey{provider: getter.Name(), instrument: "BAD"}

	service.refresh.mu.Lock()
	service.refresh.negatives[key] = negativeQuote{
		err:       errInstrument,
		expiresAt: time.Now().Add(-time.Second),
	}
	service.refresh.mu.Unlock()

	_, _ = service.GetPrice(t.Context(), getter.Name(), "BAD")
	if getter.calls != 4 {
		t.Fatalf("expired failure did not retry: %d", getter.calls)
	}

	err = service.WithCacheReset(t.Context(), func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	_, _ = service.GetPrice(t.Context(), getter.Name(), "BAD")
	if getter.calls != 5 {
		t.Fatalf("reset retained negative cache: %d", getter.calls)
	}
}

type recoveringInstrumentGetter struct {
	baseGetter

	calls int
}

func (getter *recoveringInstrumentGetter) GetPrice(context.Context, string) (models.Price, error) {
	getter.calls++
	if getter.calls == 1 {
		return models.Price{}, instrumentFailure(errInstrument)
	}

	return models.Price{Value: 25, Currency: "USD", Source: getter.Name()}, nil
}

func TestNegativeCacheDoesNotSurvivePortfolioReplacement(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "portfolio.db")
	getter := &recoveringInstrumentGetter{
		name: "Synthetic", ttl: time.Minute,
	}
	service := newRefreshTestServiceAt(t, path, getter)
	other := newRefreshTestServiceAt(t, path)

	for range 2 {
		_, err := service.GetPrice(t.Context(), getter.Name(), "TEST")
		if !errors.Is(err, errInstrument) || getter.calls != 1 {
			t.Fatalf(
				"same generation: calls=%d, error=%v; want one cached failure",
				getter.calls,
				err,
			)
		}
	}

	_, err := other.db.ExecContext(
		t.Context(),
		"UPDATE snapshot_state SET generation = generation + 1 WHERE id = 1",
	)
	if err != nil {
		t.Fatal(err)
	}

	quote, err := service.GetPrice(t.Context(), getter.Name(), "TEST")
	if err != nil || quote.Value != 25 || getter.calls != 2 {
		t.Fatalf(
			"replacement generation: quote=%+v, calls=%d, error=%v; want a fresh quote",
			quote,
			getter.calls,
			err,
		)
	}
}
