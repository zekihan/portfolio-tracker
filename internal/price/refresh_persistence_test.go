package price

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
)

func TestRefreshAllReportsCacheWriteFailure(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		reject bool
	}{
		{name: "writable cache", reject: false},
		{name: "rejected cache write", reject: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			checkRefreshCachePersistence(t, test.reject)
		})
	}
}

func checkRefreshCachePersistence(t *testing.T, reject bool) {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	getter := &refreshManualGetter{Getter: NewManual()}
	registry := NewRegistry()
	registry.Register(getter)
	service := NewService(database, registry)

	_, err = database.ExecContext(t.Context(), `INSERT INTO price_cache
  (provider, instrument_id, price, currency, source, expires_at, last_error)
  VALUES (?, '100', 10, 'TRY', 'old', ?, '')`, getter.Name(), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	if reject {
		_, err = database.ExecContext(t.Context(), `CREATE TRIGGER reject_cache_write
   BEFORE UPDATE ON price_cache BEGIN SELECT RAISE(ABORT, 'cache write rejected'); END`)
		if err != nil {
			t.Fatal(err)
		}
	}

	var holding models.Holding

	holding.PriceProvider = getter.Name()
	holding.InstrumentID = "100"

	err = service.RefreshAll(t.Context(), []models.Holding{holding})
	if reject {
		if err == nil || !strings.Contains(err.Error(), "cache write rejected") {
			t.Errorf("refresh error = %v, want cache persistence failure", err)
		}
	} else if err != nil {
		t.Fatal(err)
	}

	checkStoredRefreshQuote(t, service, getter.Name(), reject)
}

func checkStoredRefreshQuote(t *testing.T, service *Service, provider string, reject bool) {
	t.Helper()

	cached, cacheErr := service.getCachedPrice(provider, "100")
	if cacheErr != nil {
		t.Fatal(cacheErr)
	}

	want := 100.0
	if reject {
		want = 10
	}

	if cached == nil || cached.Price != want {
		t.Errorf("cached quote = %+v, want %v", cached, want)
	}

	if service.failures[provider] != 0 || service.breakers[provider] {
		t.Error("database failure counted as provider failure")
	}
}
