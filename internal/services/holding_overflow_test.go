package services

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

func TestHoldingValuationRejectsOverflow(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		currency string
		quantity float64
		current  float64
		estimate float64
		wantErr  bool
	}{
		{name: "finite", currency: "TRY", quantity: 2, current: 100, estimate: 200, wantErr: false},
		{name: "current total", currency: "TRY", quantity: math.MaxFloat64, current: 10, estimate: 0, wantErr: true},
		{name: "estimated total", currency: "TRY", quantity: math.MaxFloat64, current: 0, estimate: 10, wantErr: true},
		{name: "FX conversion", currency: "USD", quantity: 1, current: math.MaxFloat64, estimate: 0, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
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

			_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
    (name, ticker, asset_type, quantity, price_provider, instrument_id, base_currency,
     basket, location, notes, est_price, manual_price) VALUES
    ('A', '', 'Cash', ?, 'Manual', '', ?, '', '', '', ?, ?)`,
				test.quantity, test.currency, test.estimate, test.current)
			if err != nil {
				t.Fatal(err)
			}

			prices := price.NewService(database, price.DefaultRegistry())
			service := NewHoldingService(database, prices)

			holdings, err := service.GetWithPrices(t.Context(), "TRY")
			if test.wantErr {
				if err == nil {
					t.Error("accepted non-finite holding valuation")
				}

				if len(holdings) != 0 {
					t.Error("returned non-finite holdings")
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if len(holdings) != 1 {
				t.Fatalf("got %d holdings, want 1", len(holdings))
			}

			got := [4]float64{
				holdings[0].NormTotalTRY, holdings[0].NormTotalUSD,
				holdings[0].NormEstTotalTRY, holdings[0].NormEstTotalUSD,
			}
			if got != [4]float64{200, 5, 400, 10} {
				t.Errorf("unexpected holding totals: %v", got)
			}
		})
	}
}
