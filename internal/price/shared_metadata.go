package price

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// sharedMetadata keeps one immutable, bounded provider response, never errors.
// Each provider owns its caches; unrelated instances never share data.
type sharedMetadata[T any] struct {
	expires    time.Time
	fetchedAt  time.Time
	value      T
	pending    *metadataFlight[T]
	mu         sync.Mutex
	generation int64
}

type metadataFlight[T any] struct {
	done       chan struct{}
	cancel     context.CancelFunc
	err        error
	value      T
	waiters    int
	generation int64
}

func (cache *sharedMetadata[T]) get(
	ctx context.Context,
	ttl time.Duration,
	fetch func(context.Context) (T, error),
) (T, error) {
	var zero T

	err := ctx.Err()
	if err != nil {
		return zero, fmt.Errorf("metadata request: %w", err)
	}

	cache.mu.Lock()

	minimum, ok := ctx.Value(metadataRefreshKey{}).(time.Time)
	if !ok {
		minimum = time.Time{}
	}

	generation := quoteGeneration(ctx)
	if time.Now().Before(cache.expires) && !cache.fetchedAt.Before(minimum) &&
		cache.generation == generation {
		value := cache.value
		cache.mu.Unlock()

		return value, nil
	}

	flight := cache.pending
	if flight == nil || flight.generation != generation {
		// A caller leaving must not cancel requests still needed by other callers.
		workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), providerTimeout)
		flight = &metadataFlight[T]{
			done:       make(chan struct{}),
			cancel:     cancel,
			generation: generation,
		}

		cache.pending = flight

		go func() {
			defer cancel()

			cache.fetch(workCtx, flight, ttl, fetch)
		}()
	}

	flight.waiters++

	cache.mu.Unlock()
	defer cache.leave(flight)

	select {
	case <-ctx.Done():
		return zero, fmt.Errorf("metadata request: %w", ctx.Err())
	case <-flight.done:
		err := ctx.Err()
		if err != nil {
			return zero, fmt.Errorf("metadata request: %w", err)
		}

		return flight.value, flight.err
	}
}

func (cache *sharedMetadata[T]) fetch(
	ctx context.Context,
	flight *metadataFlight[T],
	ttl time.Duration,
	fetch func(context.Context) (T, error),
) {
	defer flight.cancel()

	value, err := fetchMetadataSafely(ctx, fetch)

	cache.mu.Lock()
	defer cache.mu.Unlock()

	flight.value, flight.err = value, err
	if cache.pending == flight {
		cache.pending = nil
		if err == nil && ctx.Err() == nil {
			cache.value = value
			cache.generation = flight.generation
			cache.fetchedAt = time.Now().UTC()
			cache.expires = cache.fetchedAt.Add(ttl)
		}
	}

	close(flight.done)
}

func (cache *sharedMetadata[T]) leave(flight *metadataFlight[T]) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	flight.waiters--
	if flight.waiters == 0 && cache.pending == flight {
		cache.pending = nil

		flight.cancel()
	}
}

var errMetadataFetchPanic = errors.New("provider metadata fetch panicked")

// fetchMetadataSafely keeps a provider panic from escaping the worker goroutine.
//
//nolint:nonamedreturns // Recovery must set the error returned to every waiting caller.
func fetchMetadataSafely[T any](
	ctx context.Context,
	fetch func(context.Context) (T, error),
) (value T, err error) {
	defer func() {
		if recover() != nil {
			err = errMetadataFetchPanic
		}
	}()

	return fetch(ctx)
}

// metadataRefreshKey carries one cutoff for the whole refresh batch, so all
// instruments reuse the same newly fetched provider response.
type metadataRefreshKey struct{}

func (cache *sharedMetadata[T]) reset() {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	var zero T

	cache.value = zero
	cache.expires = time.Time{}

	cache.fetchedAt = time.Time{}
	if cache.pending != nil {
		cache.pending.cancel()
		cache.pending = nil
	}
}

func (getter *PusulaPortfoy) resetMetadata() { getter.funds.reset() }

func (getter *GarantiBBVA) resetMetadata() {
	getter.date.reset()
	getter.currencies.reset()
}
