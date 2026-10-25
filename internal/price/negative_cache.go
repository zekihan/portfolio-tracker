package price

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const negativeQuoteTTL = 30 * time.Second

type negativeQuote struct {
	err       error
	expiresAt time.Time
}

// Ordinary lookups briefly reuse failures per instrument. Explicit refreshes
// may retry immediately; existing single-flight and admission bounds still apply.
func (service *Service) negativeQuoteError(provider, instrument string) error {
	state := &service.refresh
	state.mu.Lock()
	defer state.mu.Unlock()

	key := refreshKey{provider: provider, instrument: instrument}

	failure, ok := state.negatives[key]
	if !ok {
		return nil
	}

	if !time.Now().Before(failure.expiresAt) {
		delete(state.negatives, key)

		return nil
	}

	return failure.err
}

func (service *Service) rememberNegativeQuote(key refreshKey, err error) {
	if !isInstrumentFailure(err) || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return
	}

	state := &service.refresh
	state.mu.Lock()
	defer state.mu.Unlock()

	now := time.Now()
	// Bound memory even when callers cycle through arbitrary failed symbols.
	for candidate, failure := range state.negatives {
		if !now.Before(failure.expiresAt) {
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

	state.negatives[key] = negativeQuote{err: err, expiresAt: now.Add(negativeQuoteTTL)}
}

func (service *Service) forgetNegativeQuote(key refreshKey) {
	state := &service.refresh
	state.mu.Lock()
	delete(state.negatives, key)
	state.mu.Unlock()
}

func (service *Service) cachedLookupFailure(provider, instrument string, force bool) error {
	if service.breakerBlocksRequest(provider) {
		return fmt.Errorf("%w %s", errCircuitBreakerOpenFor, provider)
	}

	if force {
		return nil
	}

	return service.negativeQuoteError(provider, instrument)
}
