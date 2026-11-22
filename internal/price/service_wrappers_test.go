package price

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestServiceProviderMappingAndCacheWrappers(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	service := NewService(database, DefaultRegistry())

	t.Cleanup(func() { _ = service.Close() })

	if !service.HasProvider("Manual") {
		t.Error("HasProvider(Manual) = false, want true")
	}

	if service.HasProvider("No Such Provider") {
		t.Error("HasProvider(unknown) = true, want false")
	}

	result, err := database.ExecContext(t.Context(),
		`INSERT INTO holdings (name, asset_type, quantity) VALUES ('Cash TRY', 'Cash', 10)`)
	if err != nil {
		t.Fatal(err)
	}

	holdingID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}

	err = service.UpdateHoldingProvider(int(holdingID), "Manual", "")
	if err != nil {
		t.Fatal(err)
	}

	mappings, err := service.GetHoldingProviderMapping()
	if err != nil {
		t.Fatal(err)
	}

	if len(mappings) != 1 || mappings[0].Provider != "Manual" ||
		mappings[0].HoldingID != int(holdingID) {
		t.Errorf("mappings = %+v, want one Manual mapping for holding %d", mappings, holdingID)
	}

	err = service.UpdateHoldingProvider(int(holdingID), "No Such Provider", "")
	if !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("unknown provider error = %v, want ErrUnknownProvider", err)
	}

	err = service.UpdateHoldingProvider(99999, "Manual", "")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("missing holding error = %v, want sql.ErrNoRows", err)
	}
}

func TestClearCacheEmptiesPriceCache(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	service := NewService(database, DefaultRegistry())

	t.Cleanup(func() { _ = service.Close() })

	_, err = database.ExecContext(t.Context(),
		`INSERT INTO price_cache (provider, instrument_id, price, currency, source, expires_at)
		 VALUES ('Manual', '', 1, 'TRY', 'test', CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatal(err)
	}

	err = service.ClearCache()
	if err != nil {
		t.Fatal(err)
	}

	var cached int

	err = database.GetContext(t.Context(), &cached, "SELECT COUNT(*) FROM price_cache")
	if err != nil {
		t.Fatal(err)
	}

	if cached != 0 {
		t.Errorf("price_cache rows after ClearCache = %d, want 0", cached)
	}
}

func TestGetProvidersJSONListsDefaultRegistry(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	service := NewService(database, DefaultRegistry())

	t.Cleanup(func() { _ = service.Close() })

	providersJSON, err := service.GetProvidersJSON()
	if err != nil {
		t.Fatal(err)
	}

	var providers []map[string]any

	err = json.Unmarshal([]byte(providersJSON), &providers)
	if err != nil {
		t.Fatalf("providers JSON does not parse: %v", err)
	}

	if len(providers) != 12 {
		t.Errorf("providers = %d, want 12", len(providers))
	}
}

func TestGetEURTRYRateWithoutFrankfurter(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	service := NewService(database, NewRegistry())

	t.Cleanup(func() { _ = service.Close() })

	_, err = service.GetEURTRYRate(t.Context())
	if err == nil {
		t.Error("GetEURTRYRate with no Frankfurter provider should fail, not invent a rate")
	}
}
