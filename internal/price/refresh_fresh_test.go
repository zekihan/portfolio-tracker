package price

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
)

type refreshManualGetter struct{ Getter }

func (g *refreshManualGetter) Name() string { return "Refresh test" }

func TestRefreshAllFetchesDespiteFreshCache(t *testing.T) {
	t.Parallel()

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

	before, err := service.GetPrice(t.Context(), getter.Name(), "100")
	if err != nil {
		t.Fatal(err)
	}

	if before.Value != 10 {
		t.Fatalf("ordinary lookup bypassed fresh cache: %v", before.Value)
	}

	var holding models.Holding

	holding.PriceProvider = getter.Name()
	holding.InstrumentID = "100"

	err = service.RefreshAll(t.Context(), []models.Holding{holding})
	if err != nil {
		t.Fatal(err)
	}

	after, err := service.GetPrice(t.Context(), getter.Name(), "100")
	if err != nil {
		t.Fatal(err)
	}

	if after.Value != 100 {
		t.Errorf("explicit refresh retained old quote: %v, want 100", after.Value)
	}
}
