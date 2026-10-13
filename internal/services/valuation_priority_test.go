package services

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/price"
)

//nolint:gocyclo // Compare each row's order, quote quality, and contribution after budget exhaustion.
func TestZeroQuantityQuotesCannotStarveActiveCachedHolding(t *testing.T) {
	t.Parallel()
	database := valuationDatabase(t)
	registry := price.DefaultRegistry()
	registry.Register(&boundedValuationGetter{
		active: new(atomic.Int32), maximum: new(atomic.Int32),
		entered: make(chan struct{}, 4), release: make(chan struct{}), name: "Blocked",
	})

	_, err := database.ExecContext(t.Context(), `INSERT INTO holdings
 (name, asset_type, quantity, price_provider, instrument_id, base_currency, created_at) VALUES
 ('Closed1', 'Stock', 0, 'Blocked', 'A', 'TRY', '2026-01-02'),
 ('Closed2', 'Stock', 0, 'Blocked', 'B', 'TRY', '2026-01-02'),
 ('Closed3', 'Stock', 0, 'Blocked', 'C', 'TRY', '2026-01-02'),
 ('Closed4', 'Stock', 0, 'Blocked', 'D', 'TRY', '2026-01-02'),
 ('Active', 'Stock', 1, 'Yahoo Finance', 'GOOD', 'TRY', '2026-01-01')`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = database.ExecContext(t.Context(), `INSERT INTO price_cache
 (provider, instrument_id, price, currency, source, expires_at)
 VALUES ('Yahoo Finance', 'GOOD', 100, 'TRY', 'test', ?)`, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	prices := price.NewService(database, registry)

	t.Cleanup(func() { _ = prices.Close() })

	service := NewHoldingService(database, prices)

	expected, err := service.GetAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	rows, err := service.getWithPrices(t.Context(), "TRY", 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != len(expected) {
		t.Fatalf("lost holdings: %d", len(rows))
	}

	for index, row := range rows {
		if row.ID != expected[index].ID {
			t.Fatalf("display order changed at %d", index)
		}

		if row.Quantity == 0 {
			if row.ValuationAvailable || !row.IsFailed || row.PriceError == "" ||
				row.NormTotalTRY != 0 ||
				row.CurrentPrice != 0 {
				t.Fatalf("zero position quote quality or contribution changed: %+v", row)
			}
		} else if !row.ValuationAvailable || row.IsFailed || row.NormTotalTRY != 100 || row.NormTotalUSD != 2.5 {
			t.Fatalf("closed positions starved fresh positive quote: %+v", row)
		}
	}

	// A fresh zero-position quote still resolves when the budget permits it.
	_, err = database.ExecContext(t.Context(), `INSERT INTO price_cache
 (provider, instrument_id, price, currency, source, expires_at)
 SELECT price_provider, instrument_id, 50, 'TRY', 'test', ?
 FROM holdings WHERE quantity = 0`, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	rows, err = service.getWithPrices(t.Context(), "TRY", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	for _, row := range rows {
		if row.Quantity == 0 && (!row.ValuationAvailable || row.IsFailed ||
			row.CurrentPrice != 50 || row.NormTotalTRY != 0) {
			t.Fatalf("healthy zero-position quote was skipped: %+v", row)
		}
	}
}
