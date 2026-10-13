package services

import (
	"context"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

// EUR is normalized after core quote jobs so an optional FX lookup cannot starve
// existing TRY/USD positions. Snapshots make a short best-effort historical lookup;
// ordinary TRY/USD pages only request EUR when needed or already cached.
func (service *HoldingService) eurValuationQuote(
	ctx, requiredCtx context.Context,
	required bool,
	displayCurrency string,
) (models.Price, error) {
	if required {
		quote, err := service.priceSvc.GetEURTRYQuote(requiredCtx)

		return quote, wrapOperationError("EUR exchange rate", err)
	}

	if displayCurrency != "" {
		var cached bool

		err := service.db.GetContext(ctx, &cached, `SELECT EXISTS(SELECT 1 FROM price_cache
WHERE (provider = 'Frankfurter' AND instrument_id = 'EUR') OR
(provider = 'Yahoo Finance' AND instrument_id = 'EURTRY=X'))`)
		if err != nil {
			return models.Price{}, wrapOperationError("EUR exchange rate", err)
		}

		if !cached {
			return models.Price{}, nil
		}
	}

	const eurBudget = 2 * time.Second

	quoteCtx, cancel := context.WithTimeout(ctx, eurBudget)
	defer cancel()

	quote, err := service.priceSvc.GetEURTRYQuote(quoteCtx)

	return quote, wrapOperationError("EUR exchange rate", err)
}

func (scope valuationScope) holdingNeedsEUR(holding *models.HoldingWithPrice) bool {
	return holding.Quantity != 0 && (holding.CurrentCurrency == restoreCurrencyEUR ||
		(scope == currentAndEstimatedValues && holding.BaseCurrency == restoreCurrencyEUR && holding.EstPrice > 0))
}

func oldestFXTime(first, second time.Time) time.Time {
	if !second.IsZero() && (first.IsZero() || second.Before(first)) {
		return second
	}

	return first
}

func (scope valuationScope) requiresEURValuation(
	holdings []models.HoldingWithPrice,
	displayCurrency string,
) bool {
	if displayCurrency == restoreCurrencyEUR {
		return true
	}

	for index := range holdings {
		holding := &holdings[index]
		if holding.Quantity == 0 {
			continue
		}

		if (holding.CurrentCurrency == restoreCurrencyEUR && holding.ValuationAvailable) ||
			(scope == currentAndEstimatedValues && holding.BaseCurrency == restoreCurrencyEUR && holding.EstPrice > 0) {
			return true
		}
	}

	return false
}
