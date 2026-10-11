package price

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestCanceledRequestsDoNotOpenCircuitBreaker(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	registry := NewRegistry()
	getter := &unavailableGetter{err: context.Canceled}
	registry.Register(getter)
	service := NewService(database, registry)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	for range 3 {
		_, err = service.GetPrice(ctx, getter.Name(), "TEST")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	}

	if service.failures[getter.Name()] != 0 {
		t.Errorf("cancellations counted as %d provider failures", service.failures[getter.Name()])
	}

	if service.breakers[getter.Name()] {
		t.Error("canceled requests opened the provider circuit breaker")
	}
}
