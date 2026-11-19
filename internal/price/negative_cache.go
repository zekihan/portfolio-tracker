package price

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const negativeQuoteTTL = 30 * time.Second

type negativeQuote struct {
	err        error
	expiresAt  time.Time
	generation int64
}

// Ordinary lookups briefly reuse instrument failures from their own portfolio generation.
func (service *Service) negativeQuoteError(ctx context.Context, key refreshKey) error {
	state := &service.refresh
	state.mu.Lock()
	defer state.mu.Unlock()

	failure, ok := state.negatives[key]
	if !ok || failure.generation != quoteGeneration(ctx) {
		return nil
	}

	if !time.Now().Before(failure.expiresAt) {
		delete(state.negatives, key)

		return nil
	}

	return failure.err
}

func (service *Service) rememberNegativeQuote(ctx context.Context, key refreshKey, err error) {
	if !isInstrumentFailure(err) || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return
	}

	state := &service.refresh
	state.mu.Lock()
	defer state.mu.Unlock()

	generation := quoteGeneration(ctx)
	if existing, ok := state.negatives[key]; ok && existing.generation > generation {
		return
	}

	now := time.Now()
	// Bound memory even when callers cycle through arbitrary failed symbols.
	for candidate, failure := range state.negatives {
		if failure.generation < generation || !now.Before(failure.expiresAt) {
			delete(state.negatives, candidate)
		}
	}

	if len(state.negatives) >= maxPendingFetches {
		var (
			oldest refreshKey
			expiry time.Time
		)
		for candidate, failure := range state.negatives {
			if expiry.IsZero() || failure.expiresAt.Before(expiry) {
				oldest, expiry = candidate, failure.expiresAt
			}
		}

		delete(state.negatives, oldest)
	}

	state.negatives[key] = negativeQuote{
		err: err, expiresAt: now.Add(negativeQuoteTTL), generation: generation,
	}
}

func (service *Service) forgetNegativeQuote(ctx context.Context, key refreshKey) {
	state := &service.refresh
	state.mu.Lock()
	defer state.mu.Unlock()

	if failure, ok := state.negatives[key]; ok && failure.generation <= quoteGeneration(ctx) {
		delete(state.negatives, key)
	}
}

func (service *Service) cachedLookupFailure(ctx context.Context, key refreshKey, force bool) error {
	if service.breakerBlocksRequest(key.provider) {
		return fmt.Errorf("%w %s", errCircuitBreakerOpenFor, key.provider)
	}

	if force {
		return nil
	}

	return service.negativeQuoteError(ctx, key)
}
