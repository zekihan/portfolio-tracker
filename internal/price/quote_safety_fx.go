package price

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

type quoteSafetyHolding struct {
	Provider    string  `db:"provider"`
	Instrument  string  `db:"instrument"`
	Currency    string  `db:"currency"`
	Quantity    float64 `db:"quantity"`
	ManualPrice float64 `db:"manual_price"`
}

// FX affects manual holdings and previously cached quotes as well as quotes
// fetched under the new acceptance policy. Check them without fetching upstream
// dependencies or entering another price flight.
func (service *Service) validateFXHoldings(
	ctx context.Context, provider, instrument string, candidate *models.Price,
) error {
	currency := strings.ToUpper(strings.TrimSpace(instrument))
	if provider == "Yahoo Finance" {
		currency = strings.TrimSuffix(currency, "TRY=X")
	}

	if currency != currencyUSD && currency != currencyEUR {
		return nil
	}

	var holdings []quoteSafetyHolding

	err := service.db.SelectContext(ctx, &holdings,
		`SELECT COALESCE(price_provider, '') AS provider,
   COALESCE(instrument_id, '') AS instrument, base_currency AS currency,
   quantity, COALESCE(manual_price, 0) AS manual_price FROM holdings`)
	if err != nil {
		return fmt.Errorf("validate FX holdings: %w", err)
	}

	var entries []models.PriceCacheEntry

	err = service.db.SelectContext(ctx, &entries,
		`SELECT provider, instrument_id, currency, price FROM price_cache`)
	if err != nil {
		return fmt.Errorf("validate FX cache: %w", err)
	}

	quotes := make(map[refreshKey]models.Price, len(entries)+1)
	for index := range entries {
		entry := &entries[index]
		quotes[refreshKey{provider: entry.Provider, instrument: entry.InstrumentID}] = models.Price{
			Value: entry.Price, Currency: entry.Currency,
		}
	}

	quotes[refreshKey{provider: provider, instrument: instrument}] = *candidate
	for _, holding := range holdings {
		quote := quotes[refreshKey{
			provider:   holding.Provider,
			instrument: safeQuoteInstrument(holding.Provider, holding.Instrument),
		}]
		if holding.ManualPrice > 0 {
			quote.Value, quote.Currency = holding.ManualPrice, holding.Currency
		}

		if quote.Value > 0 && !safeFXHolding(&quote, holding.Quantity, currency, candidate.Value) {
			return errUnsafeProviderQuote
		}
	}

	return nil
}

func safeFXHolding(quote *models.Price, quantity float64, currency string, candidate float64) bool {
	for _, otherRate := range []float64{minFXRate, maxProviderQuote} {
		usdRate, eurRate := candidate, otherRate
		if currency == currencyEUR {
			usdRate, eurRate = otherRate, candidate
		}

		tryPrice, usdPrice := quote.Value, quote.Value/usdRate
		switch quote.Currency {
		case currencyUSD:
			tryPrice, usdPrice = quote.Value*usdRate, quote.Value
		case currencyEUR:
			tryPrice, usdPrice = quote.Value*eurRate, (quote.Value*eurRate)/usdRate
		case "TRY":
		default:
			continue // Unsupported currencies are already excluded from valuation.
		}

		for _, normalized := range []float64{tryPrice, usdPrice, tryPrice / eurRate} {
			total := quantity * normalized
			if math.IsInf(normalized, 0) || math.IsNaN(normalized) || math.IsInf(total, 0) ||
				math.IsNaN(total) {
				return false
			}
		}
	}

	return true
}
