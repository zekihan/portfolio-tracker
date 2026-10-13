package services

import (
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

//nolint:gocyclo // Verify all currency totals and both coverage contracts end to end.
func TestEstimatedTotalsUseEstimatesOrCurrentQuoteCurrency(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		estimates [3]any
		name      string
		wantTRY   float64
		coverage  int
	}{
		{name: "none including NULL and zero", estimates: [3]any{nil, 0, nil}, wantTRY: 2400},
		{name: "mixed", estimates: [3]any{120, nil, 10}, wantTRY: 3040, coverage: 2},
		{name: "all", estimates: [3]any{120, 20, 10}, wantTRY: 4840, coverage: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			database := valuationDatabase(t)

			_, err := database.ExecContext(t.Context(), `INSERT INTO holdings
(name, asset_type, quantity, price_provider, instrument_id, base_currency, manual_price, est_price)
VALUES ('Manual override','Cash',2,'Yahoo Finance','USDTEST','TRY',100,?),
('Dollar quote','Stock',3,'Yahoo Finance','USDTEST','EUR',0,?),
('Euro quote','Stock',4,'Yahoo Finance','EURTEST','USD',0,?)`,
				test.estimates[0], test.estimates[1], test.estimates[2])
			if err != nil {
				t.Fatal(err)
			}

			_, err = database.ExecContext(
				t.Context(),
				`INSERT INTO price_cache(provider,instrument_id,price,currency,source,expires_at,last_error)
VALUES ('Yahoo Finance','USDTEST',10,'USD','test',?,''),
('Yahoo Finance','EURTEST',5,'EUR','test',?,'')`,
				time.Now().Add(time.Hour),
				time.Now().Add(time.Hour),
			)
			if err != nil {
				t.Fatal(err)
			}

			prices := price.NewService(database, price.DefaultRegistry())

			t.Cleanup(func() { _ = prices.Close() })

			service := NewHoldingService(database, prices)

			for _, currency := range []string{"TRY", "USD", "EUR"} {
				data, getErr := service.GetDashboardData(t.Context(), currency)
				if getErr != nil {
					t.Fatal(getErr)
				}

				if data.EstimatedValueTRY != test.wantTRY ||
					data.EstimatedValueUSD != test.wantTRY/40 || data.EstimatedValueEUR != test.wantTRY/50 ||
					data.CoverageCount != test.coverage || data.EstimatedValuedCount != 3 ||
					!data.EstimatedValuationComplete || !data.ValuationComplete {
					t.Fatalf("%s estimate totals/coverage: %+v", currency, data)
				}

				if data.CurrentValueTRY != 2400 || data.CurrentValueUSD != 60 ||
					data.CurrentValueEUR != 48 {
					t.Fatalf("current totals changed: %+v", data)
				}
			}
		})
	}
}

//nolint:gocyclo // Partial current and estimated totals have independent availability contracts.
func TestEstimatedAvailabilityIncludesZeroPositionsButNotMissingQuotes(t *testing.T) {
	t.Parallel()
	database := valuationDatabase(t)

	_, err := database.ExecContext(t.Context(), `INSERT INTO holdings
(name,asset_type,quantity,base_currency,manual_price,est_price) VALUES
('No quote or estimate','Cash',2,'TRY',0,NULL),
('Estimate without quote','Cash',2,'USD',0,10),
('Closed position','Cash',0,'EUR',0,NULL)`)
	if err != nil {
		t.Fatal(err)
	}

	prices := price.NewService(database, price.DefaultRegistry())

	t.Cleanup(func() { _ = prices.Close() })

	service := NewHoldingService(database, prices)

	rows, err := service.GetWithPrices(t.Context(), "EUR")
	if err != nil {
		t.Fatal(err)
	}

	for _, row := range rows {
		if row.ValuationAvailable || !row.IsFailed ||
			row.EstimatedValuationAvailable != (row.Name != "No quote or estimate") {
			t.Fatalf("incorrect availability: %+v", row)
		}
	}

	data, err := service.GetDashboardData(t.Context(), "EUR")
	if err != nil {
		t.Fatal(err)
	}

	if data.ValuedCount != 1 || data.UnvaluedCount != 2 || data.ValuationComplete ||
		data.EstimatedValuedCount != 2 || data.EstimatedValuationComplete || data.CoverageCount != 1 ||
		data.EstimatedValueTRY != 800 || data.EstimatedValueUSD != 20 || data.EstimatedValueEUR != 16 {
		t.Fatalf("incorrect partial totals: %+v", data)
	}
}

func TestNormalizeEstimatedFallbackReplacesPriorValues(t *testing.T) {
	t.Parallel()

	service := &HoldingService{}
	row := models.HoldingWithPrice{
		Quantity: 2, BaseCurrency: "EUR", CurrentCurrency: "USD", CurrentPrice: 10,
		EstPrice: 20, ValuationAvailable: true,
	}

	for _, test := range []struct {
		estimate  float64
		available bool
		wantTRY   float64
	}{
		{estimate: 20, available: true, wantTRY: 2000},
		{estimate: 0, available: true, wantTRY: 800},
		{estimate: 0, available: true, wantTRY: 800},
		{estimate: 0, available: false, wantTRY: 0},
	} {
		row.EstPrice = test.estimate
		row.ValuationAvailable = test.available

		err := service.normalizeHolding(&row, 40, 50)
		if err != nil {
			t.Fatal(err)
		}

		if row.NormEstTotalTRY != test.wantTRY || row.NormEstTotalUSD != test.wantTRY/40 ||
			row.NormEstTotalEUR != test.wantTRY/50 || row.EstimatedValuationAvailable != test.available {
			t.Fatalf("normalization accumulated or retained old values: %+v", row)
		}
	}
}
