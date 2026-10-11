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

var errProviderUnavailable = errors.New("provider unavailable")

type unavailableGetter struct {
	err error
}

func (g *unavailableGetter) GetPrice(context.Context, string) (models.Price, error) {
	var result models.Price

	return result, g.err
}

func (g *unavailableGetter) Name() string       { return "Unavailable" }
func (g *unavailableGetter) Type() string       { return "Stock/ETF" }
func (g *unavailableGetter) TTL() time.Duration { return time.Minute }

func TestGetPriceDoesNotUseFailedCacheRead(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	err = database.Close()
	if err != nil {
		t.Fatal(err)
	}

	providerErr := errProviderUnavailable
	registry := NewRegistry()
	getter := &unavailableGetter{err: providerErr}
	registry.Register(getter)
	service := NewService(database, registry)

	_, err = service.GetPrice(t.Context(), getter.Name(), "TEST")
	if !errors.Is(err, providerErr) {
		t.Fatalf("got error %v, want provider failure instead of a phantom cached price", err)
	}
}
