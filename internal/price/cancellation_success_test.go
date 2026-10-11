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

type cancelingSuccessGetter struct {
	cancel context.CancelFunc
}

func (g *cancelingSuccessGetter) Name() string       { return "Canceling success" }
func (g *cancelingSuccessGetter) Type() string       { return "Stock/ETF" }
func (g *cancelingSuccessGetter) TTL() time.Duration { return time.Minute }
func (g *cancelingSuccessGetter) GetPrice(context.Context, string) (models.Price, error) {
	g.cancel()

	return models.Price{
		Value: 100, Currency: "USD", Source: g.Name(), FetchedAt: time.Now(),
		Error: nil, IsStale: false, IsFailed: false,
	}, nil
}

func TestSuccessfulFetchRespectsCancellation(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	getter := &cancelingSuccessGetter{cancel: cancel}
	registry := NewRegistry()
	registry.Register(getter)
	service := NewService(database, registry)
	service.failures[getter.Name()] = 2

	_, err = service.GetPrice(ctx, getter.Name(), "TEST")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want cancellation", err)
	}

	if service.failures[getter.Name()] != 2 {
		t.Error("canceled fetch reset provider failures")
	}

	cached, err := service.getCachedPrice(getter.Name(), "TEST")
	if err != nil {
		t.Fatal(err)
	}

	if cached != nil {
		t.Error("canceled fetch persisted a cache entry")
	}
}
