package services

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

func TestHoldingValuationRequiresFXRate(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	priceService := price.NewService(database, price.NewRegistry())
	service := NewHoldingService(database, priceService)

	_, err = service.GetWithPrices(t.Context(), "TRY")
	if err == nil {
		t.Fatal("holding valuation succeeded without an FX rate")
	}
}

func TestSnapshotRequiresFXRate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		registry  *price.Registry
		name      string
		wantCount int
	}{
		{registry: price.NewRegistry(), name: "unavailable", wantCount: 0},
		{registry: price.DefaultRegistry(), name: "available", wantCount: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			_, err = database.ExecContext(t.Context(), `
				INSERT INTO price_cache
				(provider, instrument_id, price, currency, source, expires_at, last_error)
				VALUES ('Frankfurter', 'USD', 40, 'TRY', 'test', ?, '')
			`, time.Now().UTC().Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}

			// The holding provider is valid even when no FX provider is registered.
			test.registry.Register(price.NewManual())
			priceService := price.NewService(database, test.registry)
			holdingService := NewHoldingService(database, priceService)

			var holding models.Holding

			holding.Name = "Cash"
			holding.AssetType = models.AssetTypeCash
			holding.BaseCurrency = "TRY"
			holding.Quantity = 2

			holding.PriceProvider = "Manual"

			err = holdingService.Create(t.Context(), &holding)
			if err != nil {
				t.Fatal(err)
			}

			err = holdingService.UpdateManualPrice(t.Context(), holding.ID, 10)
			if err != nil {
				t.Fatal(err)
			}

			service := NewSnapshotService(database, priceService, holdingService)

			available := test.wantCount == 1

			err = service.CreateSnapshot(t.Context())
			if (err == nil) != available {
				t.Errorf("snapshot error = %v, FX available: %v", err, available)
			}

			snapshots, err := service.GetSnapshots(t.Context(), 10, 0, 0)
			if err != nil {
				t.Fatal(err)
			}

			wantCount := test.wantCount

			if len(snapshots) != wantCount {
				t.Fatalf("saved %d snapshots, want %d", len(snapshots), wantCount)
			}

			if available && (snapshots[0].TotalValueTRY != 20 ||
				snapshots[0].TotalValueUSD != 0.5 || snapshots[0].USDTRYRate != 40) {
				t.Fatalf("incorrect snapshot: %+v", snapshots[0])
			}
		})
	}
}
