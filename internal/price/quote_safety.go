package price

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

// One trillion units per asset and FX rates between 1e-12 and 1e12 leave
// generous market-data headroom while excluding extreme finite anomalies.
// These are acceptance limits, not clamping: preserve the old quote on failure.
const (
	maxProviderQuote = 1e12
	minFXRate        = 1e-12
)

var errUnsafeProviderQuote = errors.New("provider quote exceeds valuation safety limits")

func isFXInstrument(provider, instrument string) bool {
	instrument = strings.ToUpper(strings.TrimSpace(instrument))

	return provider == "Frankfurter" || (provider == "Yahoo Finance" &&
		(instrument == "USDTRY=X" || instrument == "EURTRY=X"))
}

// Check against every accepted FX rate, not only today's cached sample: parallel
// FX refreshes must not make an already accepted holding quote overflow later.
func safeHoldingQuote(value, quantity float64, currency string) bool {
	if quantity < 0 || math.IsNaN(quantity) || math.IsInf(quantity, 0) {
		return false
	}

	maximumPrice := value
	if currency == currencyUSD || currency == currencyEUR {
		maximumPrice *= maxProviderQuote
	}

	maximumPrice /= minFXRate

	return !math.IsInf(maximumPrice, 0) && !math.IsInf(quantity*maximumPrice, 0)
}

func (service *Service) validateFetchedQuote(
	ctx context.Context, provider, instrument string, quote *models.Price,
) error {
	if !safeFetchedQuoteValue(provider, instrument, quote.Value) {
		return errUnsafeProviderQuote
	}

	if isFXInstrument(provider, instrument) {
		err := service.validateFXHoldings(ctx, provider, instrument, quote)
		if err != nil {
			return err
		}
	}

	// Generation-guarded cache writes prevent a concurrent portfolio mutation
	// from promoting a quote checked against an obsolete holding quantity.
	var holdings []struct {
		Instrument string  `db:"instrument_id"`
		Quantity   float64 `db:"quantity"`
	}

	err := service.db.SelectContext(ctx, &holdings,
		`SELECT COALESCE(instrument_id, '') AS instrument_id, quantity
   FROM holdings WHERE price_provider = ? AND COALESCE(manual_price, 0) <= 0`, provider)
	if err != nil {
		return fmt.Errorf("validate quote holdings: %w", err)
	}

	for _, holding := range holdings {
		if safeQuoteInstrument(provider, holding.Instrument) == instrument &&
			!safeHoldingQuote(quote.Value, holding.Quantity, quote.Currency) {
			return errUnsafeProviderQuote
		}
	}

	return nil
}

func checkedInstrumentQuote(
	value float64,
	currency, provider, instrument string,
) (models.Price, error) {
	if isFXInstrument(provider, instrument) && value < minFXRate {
		return models.Price{}, errUnsafeProviderQuote
	}

	return checkedQuote(value, currency, provider)
}

func safeFetchedQuoteValue(provider, instrument string, value float64) bool {
	return value >= 0 && (value > 0 || provider == manualProviderName) &&
		value <= maxProviderQuote && !math.IsNaN(value) && !math.IsInf(value, 0) &&
		(!isFXInstrument(provider, instrument) || value >= minFXRate)
}
