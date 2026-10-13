package price

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

const currencyEUR = "EUR"

func (service *Service) getUSDTTRYQuote(ctx context.Context, force bool) (models.Price, error) {
	return service.getFXTRYQuote(ctx, "USD", force)
}

func (service *Service) getFXTRYQuote(
	ctx context.Context,
	currency string,
	force bool,
) (models.Price, error) {
	primary, primaryErr := service.getPrice(ctx, "Frankfurter", currency, force)
	if usableFXQuote(&primary, primaryErr) && !primary.IsFailed && !primary.IsStale {
		return primary, nil
	}

	if !force && usableFXQuote(&primary, primaryErr) {
		return service.recoverCachedFX(ctx, currency, &primary)
	}

	fallback, fallbackErr := service.getPrice(ctx, "Yahoo Finance", currency+"TRY=X", force)
	if usableFXQuote(&fallback, fallbackErr) && !fallback.IsFailed && !fallback.IsStale {
		return fallback, nil
	}

	if !force && usableFXQuote(&fallback, fallbackErr) {
		return fallback, nil
	}

	return models.Price{}, fmt.Errorf("no fresh %s/TRY FX rate available: %w", currency,
		errors.Join(errRefresh, primaryErr, primary.Error, fallbackErr, fallback.Error))
}

func usableFXQuote(quote *models.Price, err error) bool {
	return err == nil && quote.Currency == "TRY" &&
		quote.Value > 0 && !math.IsInf(quote.Value, 0) && !math.IsNaN(quote.Value)
}

func (service *Service) recoverCachedFX(
	ctx context.Context,
	currency string,
	primary *models.Price,
) (models.Price, error) {
	fallback, err := service.backgroundFXQuote(ctx, currency)
	if ctx.Err() != nil {
		return models.Price{}, wrapOperationError("getFXTRYQuote", ctx.Err())
	}

	// Prefer a healthy fallback, or the newer cached sample while both refresh.
	if usableFXQuote(&fallback, err) &&
		((!fallback.IsFailed && !fallback.IsStale) || fallback.FetchedAt.After(primary.FetchedAt)) {
		return fallback, nil
	}

	return *primary, nil
}

// Keep serving stale FX immediately, but recover through the same bounded,
// coalesced fetch machinery even when the fallback cache has never been populated.
func (service *Service) backgroundFXQuote(
	ctx context.Context,
	currency string,
) (models.Price, error) {
	ctx, err := service.withQuoteGeneration(ctx)
	if err != nil {
		return models.Price{}, err
	}

	return service.coalescedPrice(ctx, "Yahoo Finance", currency+"TRY=X", false, true)
}
