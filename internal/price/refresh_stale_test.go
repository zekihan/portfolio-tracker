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

	for _, test := range []struct {
		name string
		age  time.Duration
	}{
		{name: "expired cache", age: -time.Hour},
		{name: "fresh cache", age: time.Hour},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			checkRefreshFallback(t, test.age)
		})
	}
}

func checkRefreshFallback(t *testing.T, age time.Duration) {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	getter := &unavailableGetter{err: errProviderUnavailable}
	registry := NewRegistry()
	registry.Register(getter)
	service := NewService(database, registry)

	t.Cleanup(func() { _ = service.Close() })

	_, err = database.ExecContext(t.Context(), `
		INSERT INTO price_cache
		(provider, instrument_id, price, currency, source, expires_at, last_error)
		VALUES (?, 'TEST', 10, 'USD', 'test', ?, '')
	`, getter.Name(), time.Now().UTC().Add(age))
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

	wantStale := age < 0
	if cached.Value != 10 || cached.IsStale != wantStale || !cached.IsFailed {
		t.Fatalf("stale fallback was not preserved: %+v", cached)
	}
}
