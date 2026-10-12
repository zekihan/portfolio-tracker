package services

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

type cancelingQuoteGetter struct {
	cancel context.CancelFunc
	name   string
}

func (g *cancelingQuoteGetter) GetPrice(context.Context, string) (models.Price, error) {
	g.cancel()

	return models.Price{}, io.ErrUnexpectedEOF
}
func (g *cancelingQuoteGetter) Name() string       { return g.name }
func (g *cancelingQuoteGetter) Type() string       { return "Stock/ETF" }
func (g *cancelingQuoteGetter) TTL() time.Duration { return time.Minute }

func TestValuationPropagatesMidFetchCancellation(t *testing.T) {
	t.Parallel()

	for _, canceled := range []bool{true, false} {
		name := "provider outage"
		if canceled {
			name = "canceled"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			_, err = database.ExecContext(t.Context(), `INSERT INTO price_cache
				(provider, instrument_id, price, currency, source, expires_at)
				VALUES ('Frankfurter', 'USD', 40, 'TRY', 'test', ?)`, time.Now().UTC().Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			getter := &cancelingQuoteGetter{cancel: func() {}, name: "Canceling"}
			if canceled {
				getter.cancel = cancel
			}

			registry := price.NewRegistry()
			registry.Register(&cancelingQuoteGetter{name: "Frankfurter", cancel: func() {
				t.Error("fresh legacy FX cache unexpectedly bypassed")
			}})
			registry.Register(getter)
			priceService := price.NewService(database, registry)

			t.Cleanup(func() { _ = priceService.Close() })

			service := NewHoldingService(database, priceService)

			var holding models.Holding

			holding.Name = "Stock"
			holding.AssetType = models.AssetTypeStock
			holding.BaseCurrency = "USD"
			holding.Quantity = 1
			holding.PriceProvider = getter.Name()
			holding.InstrumentID = "TEST"

			err = service.Create(t.Context(), &holding)
			if err != nil {
				t.Fatal(err)
			}

			holdings, err := service.GetWithPrices(ctx, "TRY")
			if canceled {
				if !errors.Is(err, context.Canceled) || holdings != nil {
					t.Fatalf("got %v, %v; want no holdings and cancellation", holdings, err)
				}
			} else if err != nil || len(holdings) != 1 || !holdings[0].IsFailed {
				t.Fatalf("ordinary outage behavior changed: %v, %v", holdings, err)
			}
		})
	}
}
