package services

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

func TestValuationRejectsUnsupportedQuoteCurrencies(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		currency    string
		wantTRY     float64
		wantUSD     float64
		unavailable bool
	}{
		{currency: "EUR", wantTRY: 0, wantUSD: 0, unavailable: true},
		{currency: "TRY", wantTRY: 100, wantUSD: 2.5, unavailable: false},
		{currency: "USD", wantTRY: 4000, wantUSD: 100, unavailable: false},
	} {
		t.Run(test.currency, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
    (name, ticker, asset_type, quantity, price_provider, instrument_id,
     base_currency, basket, location, notes, est_price)
    VALUES ('Test', '', 'Stock', 1, 'Yahoo Finance', 'TEST', 'TRY', '', '', '', 0)`)
			if err != nil {
				t.Fatal(err)
			}

			expires := time.Now().UTC().Add(time.Hour)

			_, err = database.ExecContext(t.Context(), `INSERT INTO price_cache
    (provider, instrument_id, price, currency, source, expires_at, last_error) VALUES
    ('Frankfurter', 'USD', 40, 'TRY', 'test', ?, ''),
    ('Yahoo Finance', 'TEST', 100, ?, 'test', ?, '')`, expires, test.currency, expires)
			if err != nil {
				t.Fatal(err)
			}

			prices := price.NewService(database, price.DefaultRegistry())
			service := NewHoldingService(database, prices)

			holdings, err := service.GetWithPrices(t.Context(), "TRY")
			if err != nil {
				t.Fatal(err)
			}

			if len(holdings) != 1 {
				t.Fatalf("got %d holdings, want 1", len(holdings))
			}

			if holdings[0].ValuationAvailable == test.unavailable ||
				holdings[0].IsFailed != test.unavailable {
				t.Fatalf("incorrect availability/failure flags: %+v", holdings[0])
			}

			if test.unavailable && holdings[0].PriceError == "" {
				t.Fatal("unavailable quote lacks an explanation")
			}

			if [2]float64{holdings[0].NormTotalTRY, holdings[0].NormTotalUSD} !=
				[2]float64{test.wantTRY, test.wantUSD} {
				t.Errorf("totals = %v TRY/%v USD, want %v TRY/%v USD",
					holdings[0].NormTotalTRY, holdings[0].NormTotalUSD, test.wantTRY, test.wantUSD)
			}
		})
	}
}
