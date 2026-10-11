package services

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

func TestSnapshotDoesNotPersistUnserializableHoldings(t *testing.T) {
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

	priceService := price.NewService(database, price.DefaultRegistry())
	holdingService := NewHoldingService(database, priceService)

	var holding models.Holding

	holding.Name = "Overflow"
	holding.AssetType = models.AssetTypeCash
	holding.BaseCurrency = "TRY"
	holding.Quantity = math.MaxFloat64

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

	err = service.CreateSnapshot(t.Context())
	if err == nil {
		t.Error("snapshot succeeded despite an infinite holding value")
	}

	snapshots, err := service.GetSnapshots(t.Context(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(snapshots) != 0 {
		t.Fatalf("saved %d corrupt snapshots, want none", len(snapshots))
	}
}
