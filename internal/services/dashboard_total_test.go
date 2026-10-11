package services

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

func TestDashboardRejectsOverflowingTotals(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		current  float64
		estimate float64
		wantErr  bool
	}{
		{name: "finite", current: 100, estimate: 200, wantErr: false},
		{name: "current overflow", current: math.MaxFloat64 * 0.75, estimate: 0, wantErr: true},
		{name: "estimated overflow", current: 100, estimate: math.MaxFloat64 * 0.75, wantErr: true},
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
    ('A', '', 'Cash', 1, 'Manual', '', 'TRY', '', '', '', ?, ?),
    ('B', '', 'Cash', 1, 'Manual', '', 'TRY', '', '', '', ?, ?)`,
				test.estimate, test.current, test.estimate, test.current)
			if err != nil {
				t.Fatal(err)
			}

			prices := price.NewService(database, price.DefaultRegistry())
			service := NewHoldingService(database, prices)

			dashboard, err := service.GetDashboardData(t.Context(), "TRY")
			if test.wantErr {
				if err == nil {
					t.Error("accepted infinite aggregate dashboard total")
				}

				if dashboard != nil {
					t.Error("returned non-finite dashboard")
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if dashboard == nil {
				t.Fatal("missing valid dashboard")
			}

			got := [4]float64{
				dashboard.CurrentValueTRY, dashboard.CurrentValueUSD,
				dashboard.EstimatedValueTRY, dashboard.EstimatedValueUSD,
			}
			if got != [4]float64{200, 5, 400, 10} {
				t.Errorf("unexpected dashboard totals: %v", got)
			}
		})
	}
}
