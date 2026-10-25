package price

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
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
