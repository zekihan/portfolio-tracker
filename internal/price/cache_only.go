package price

import (
	"context"
	"errors"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

type cachedQuotesOnlyKey struct{}

var errQuoteNotCached = errors.New("quote unavailable in cache; refresh prices explicitly")

// WithCachedQuotesOnly prevents quote lookups, including FX fallbacks, from
// initiating network work. Stale cached quotes retain their freshness metadata.
func WithCachedQuotesOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, cachedQuotesOnlyKey{}, true)
}

func cachedQuotesOnly(ctx context.Context) bool {
	return ctx.Value(cachedQuotesOnlyKey{}) != nil
}

func readOnlyCachedQuote(cached *models.PriceCacheEntry, err error) (models.Price, error) {
	if err != nil {
		return models.Price{}, err
	}

	if cached == nil {
		return models.Price{}, errQuoteNotCached
	}

	return cachedQuote(cached, cached.ExpiresAt.Before(time.Now())), nil
}

func failedCachedQuote(cached *models.PriceCacheEntry, failure error) (models.Price, error) {
	if cached == nil {
		return models.Price{}, failure
	}

	quote := cachedQuote(cached, true)
	quote.IsFailed, quote.Error = true, failure

	return quote, nil
}
