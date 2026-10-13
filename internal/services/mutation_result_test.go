package services

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

func TestHoldingMutationMissingRows(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	service := NewHoldingService(database, nil)

	for _, test := range []struct {
		run  func() error
		name string
	}{
		{name: "update", run: func() error { return service.Update(t.Context(), &models.Holding{ID: 999}) }},
		{name: "estimate", run: func() error { return service.UpdateEstPrice(t.Context(), 999, 1) }},
		{name: "manual", run: func() error { return service.UpdateManualPrice(t.Context(), 999, 1) }},
		{name: "clear", run: func() error { return service.ClearManualPrice(t.Context(), 999) }},
		{name: "delete", run: func() error { return service.Delete(t.Context(), 999) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			mutationErr := test.run()
			if !errors.Is(mutationErr, sql.ErrNoRows) {
				t.Fatalf("error=%v, want sql.ErrNoRows", mutationErr)
			}
		})
	}

	holding := models.Holding{
		Name:         "Original",
		BaseCurrency: "TRY",
		AssetType:    models.AssetTypeCash,
	}

	err = service.Create(t.Context(), &holding)
	if err != nil {
		t.Fatal(err)
	}

	for range 2 {
		err = service.Update(t.Context(), &holding)
		if err != nil {
			t.Fatal(err)
		}

		err = service.UpdateEstPrice(t.Context(), holding.ID, 0)
		if err != nil {
			t.Fatal(err)
		}

		err = service.UpdateManualPrice(t.Context(), holding.ID, 0)
		if err != nil {
			t.Fatal(err)
		}

		err = service.ClearManualPrice(t.Context(), holding.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestHoldingServiceRejectsUnknownProviders(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	service := NewHoldingService(database, price.NewService(database, price.DefaultRegistry()))

	holding := models.Holding{
		Name:          "Original",
		BaseCurrency:  "TRY",
		AssetType:     models.AssetTypeCash,
		PriceProvider: "Unknown",
	}

	err = service.Create(t.Context(), &holding)
	if !errors.Is(err, price.ErrUnknownProvider) {
		t.Fatalf("create error=%v", err)
	}

	holding.PriceProvider = "Manual"

	err = service.Create(t.Context(), &holding)
	if err != nil {
		t.Fatal(err)
	}

	holding.PriceProvider = "Unknown"

	err = service.Update(t.Context(), &holding)
	if !errors.Is(err, price.ErrUnknownProvider) {
		t.Fatalf("update error=%v", err)
	}

	stored, err := service.GetByID(t.Context(), holding.ID)
	if err != nil {
		t.Fatal(err)
	}

	if stored.PriceProvider != "Manual" {
		t.Fatal("invalid update changed provider")
	}
}
