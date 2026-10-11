package services

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
)

func TestHoldingOperationsRespectCancellation(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	service := NewHoldingService(database, nil)

	var holding models.Holding

	holding.Name = "Cash"
	holding.AssetType = models.AssetTypeCash
	holding.BaseCurrency = "TRY"

	holding.EstPrice = 10
	err = service.Create(t.Context(), &holding)
	if err != nil {
		t.Fatal(err)
	}

	err = service.UpdateManualPrice(t.Context(), holding.ID, 20)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	operations := []struct {
		run  func() error
		name string
	}{
		{name: "list", run: func() error {
			_, e := service.GetAll(ctx)
			return e
		}},
		{name: "get", run: func() error {
			_, e := service.GetByID(ctx, holding.ID)
			return e
		}},
		{name: "create", run: func() error {
			h := holding
			return service.Create(ctx, &h)
		}},
		{name: "update", run: func() error {
			h := holding
			h.Name = "Changed"

			return service.Update(ctx, &h)
		}},
		{name: "estimate", run: func() error {
			return service.UpdateEstPrice(ctx, holding.ID, 30)
		}},
		{name: "manual", run: func() error {
			return service.UpdateManualPrice(ctx, holding.ID, 40)
		}},
		{name: "clear", run: func() error { return service.ClearManualPrice(ctx, holding.ID) }},
		{name: "delete", run: func() error { return service.Delete(ctx, holding.ID) }},
	}

	for _, operation := range operations {
		err = operation.run()
		if !errors.Is(err, context.Canceled) {
			t.Errorf("%s: got %v, want context.Canceled", operation.name, err)
		}
	}

	holdings, err := service.GetAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if len(holdings) != 1 {
		t.Fatalf("canceled operations changed holding count to %d", len(holdings))
	}

	if holdings[0].Name != "Cash" || holdings[0].EstPrice != 10 ||
		!holdings[0].ManualPrice.Valid || holdings[0].ManualPrice.Float64 != 20 {
		t.Fatal("canceled operations changed the persisted holding")
	}
}
