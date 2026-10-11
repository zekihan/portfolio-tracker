package services

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

func TestDashboardTotalsPreserveCurrency(t *testing.T) {
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
	service := NewHoldingService(database, priceService)
	ctx := t.Context()

	for _, currency := range []string{"TRY", "USD"} {
		var holding models.Holding

		holding.Name = currency
		holding.AssetType = models.AssetTypeCash
		holding.BaseCurrency = currency
		holding.Quantity = 2
		holding.EstPrice = 20
		holding.PriceProvider = "Manual"

		err = service.Create(ctx, &holding)
		if err != nil {
			t.Fatal(err)
		}

		err = service.UpdateManualPrice(ctx, holding.ID, 10)
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, currency := range []string{"TRY", "USD"} {
		t.Run(currency, func(t *testing.T) {
			t.Parallel()

			data, getErr := service.GetDashboardData(t.Context(), currency)
			if getErr != nil {
				t.Fatal(getErr)
			}

			if data.CurrentValueTRY != 820 || data.CurrentValueUSD != 20.5 {
				t.Errorf("current totals: TRY=%v USD=%v, want 820 and 20.5",
					data.CurrentValueTRY, data.CurrentValueUSD)
			}

			if data.EstimatedValueTRY != 1640 || data.EstimatedValueUSD != 41 {
				t.Errorf("estimated totals: TRY=%v USD=%v, want 1640 and 41",
					data.EstimatedValueTRY, data.EstimatedValueUSD)
			}

			wantAllocation := data.CurrentValueTRY
			if currency == "USD" {
				wantAllocation = data.CurrentValueUSD
			}

			if data.AllocationByType["Cash"] != wantAllocation {
				t.Errorf("allocation = %v, want %v",
					data.AllocationByType["Cash"], wantAllocation)
			}
		})
	}
}

func TestEstimateCoverageExcludesCurrentPrices(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.ExecContext(t.Context(), `INSERT INTO price_cache
 (provider, instrument_id, price, currency, source, expires_at, last_error)
 VALUES ('Frankfurter', 'USD', 40, 'TRY', 'test', ?, '')`, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	service := NewHoldingService(database, price.NewService(database, price.DefaultRegistry()))

	for _, estimate := range []float64{0, 20} {
		var holding models.Holding

		holding.Name = "Cash"
		holding.AssetType = models.AssetTypeCash
		holding.BaseCurrency = "TRY"
		holding.Quantity = 1
		holding.PriceProvider = "Manual"
		holding.EstPrice = estimate

		err = service.Create(t.Context(), &holding)
		if err != nil {
			t.Fatal(err)
		}

		err = service.UpdateManualPrice(t.Context(), holding.ID, 10)
		if err != nil {
			t.Fatal(err)
		}
	}

	data, err := service.GetDashboardData(t.Context(), "TRY")
	if err != nil {
		t.Fatal(err)
	}

	if data.CoverageCount != 1 || data.TotalHoldings != 2 || data.EstimatedValueTRY != 20 {
		t.Errorf("unexpected estimate coverage: %+v", data)
	}
}
