package price

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
)

func TestRefreshAllReportsStaleFallback(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	getter := &unavailableGetter{err: errProviderUnavailable}
	registry := NewRegistry()
	registry.Register(getter)
	service := NewService(database, registry)

	_, err = database.ExecContext(t.Context(), `
		INSERT INTO price_cache
		(provider, instrument_id, price, currency, source, expires_at, last_error)
		VALUES (?, 'TEST', 10, 'USD', 'test', ?, '')
	`, getter.Name(), time.Now().UTC().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	var holding models.Holding

	holding.PriceProvider = getter.Name()
	holding.InstrumentID = "TEST"

	err = service.RefreshAll(t.Context(), []models.Holding{holding})
	if err == nil {
		t.Error("refresh reported success after falling back to stale data")
	} else if !strings.Contains(err.Error(), getter.Name()) {
		t.Errorf("refresh failure does not identify provider: %v", err)
	}

	cached, err := service.GetPrice(t.Context(), getter.Name(), "TEST")
	if err != nil {
		t.Fatal(err)
	}

	if cached.Value != 10 || !cached.IsStale || !cached.IsFailed {
		t.Fatalf("stale fallback was not preserved: %+v", cached)
	}
}
