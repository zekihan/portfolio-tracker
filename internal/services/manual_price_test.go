package services

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

func assertManualOverride(t *testing.T, holding *models.HoldingWithPrice) {
	t.Helper()

	if holding.CurrentPrice != 20 || holding.CurrentCurrency != "TRY" ||
		holding.PriceSource != "Manual" || holding.NormTotalTRY != 40 ||
		holding.NormTotalUSD != 1 || holding.PriceFetchedAt.IsZero() {
		t.Errorf("manual override not applied: %+v", holding)
	}
}

func TestManualPriceOverridesProvider(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.ExecContext(t.Context(), `
		INSERT INTO price_cache
		(provider, instrument_id, price, currency, source, expires_at, last_error)
		VALUES ('Frankfurter', 'USD', 40, 'TRY', 'test', ?, ''),
		('Yahoo Finance', 'TEST', 100, 'USD', 'Yahoo Finance', ?, '')
	`, time.Now().UTC().Add(time.Hour), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	priceService := price.NewService(database, price.DefaultRegistry())
	service := NewHoldingService(database, priceService)

	var holding models.Holding

	holding.Name = "Stock"
	holding.AssetType = models.AssetTypeStock
	holding.BaseCurrency = "TRY"
	holding.Quantity = 2
	holding.PriceProvider = "Yahoo Finance"

	holding.InstrumentID = "TEST"

	err = service.Create(t.Context(), &holding)
	if err != nil {
		t.Fatal(err)
	}

	err = service.UpdateManualPrice(t.Context(), holding.ID, 20)
	if err != nil {
		t.Fatal(err)
	}

	holdings, err := service.GetWithPrices(t.Context(), "TRY")
	if err != nil {
		t.Fatal(err)
	}

	if len(holdings) != 1 {
		t.Fatalf("got %d holdings, want 1", len(holdings))
	}

	assertManualOverride(t, &holdings[0])

	err = service.ClearManualPrice(t.Context(), holding.ID)
	if err != nil {
		t.Fatal(err)
	}

	holdings, err = service.GetWithPrices(t.Context(), "TRY")
	if err != nil {
		t.Fatal(err)
	}

	if len(holdings) != 1 {
		t.Fatalf("got %d holdings after clearing, want 1", len(holdings))
	}

	if holdings[0].CurrentPrice != 100 || holdings[0].CurrentCurrency != "USD" ||
		holdings[0].PriceSource != "Yahoo Finance" {
		t.Errorf("provider pricing not restored: %+v", holdings[0])
	}
}
