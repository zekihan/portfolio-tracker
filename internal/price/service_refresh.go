package price

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
)

const (
	maxConcurrentFetches = 4
	maxPendingFetches    = 128
	refreshTimeout       = 30 * time.Second
)

var (
	errServiceClosed    = errors.New("price service closed")
	errRefreshQueueFull = errors.New("price refresh queue full")
)

type (
	refreshKey  struct{ provider, instrument string }
	priceFlight struct {
		ctx        context.Context //nolint:containedctx // Shared operation lifetime is independent of each caller.
		err        error
		cancel     context.CancelFunc
		done       chan struct{}
		waiters    map[uint64]context.Context
		quote      models.Price
		background bool
	}
)

type refreshState struct {
	ctx        context.Context //nolint:containedctx // Service owns and cancels this refresh generation.
	cancel     context.CancelFunc
	flights    map[refreshKey]*priceFlight
	providers  map[string]chan struct{}
	slots      chan struct{}
	gate       sync.RWMutex
	mu         sync.Mutex
	workers    sync.WaitGroup
	nextWaiter uint64
	probes     int
	closed     bool
}

func (service *Service) initRefresh() {
	ctx, cancel := context.WithCancel(context.Background())
	service.refresh.ctx, service.refresh.cancel = ctx, cancel //nolint:fatcontext // Install the service-owned lifetime.
	service.refresh.flights = make(map[refreshKey]*priceFlight)
	service.refresh.providers = make(map[string]chan struct{})
	service.refresh.slots = make(chan struct{}, maxConcurrentFetches)
}

// Close stops refreshes and joins them before the caller closes the database.
func (service *Service) Close() error {
	state := &service.refresh
	state.gate.Lock()
	defer state.gate.Unlock()

	state.closed = true
	state.cancel()
	state.workers.Wait()

	return nil
}

// WithCacheReset excludes new readers/refreshes and joins old refreshes before
// replacing the cache. Keep the entire clear or restore operation in reset.
func (service *Service) WithCacheReset(ctx context.Context, reset func() error) error {
	state := &service.refresh

	gateErr := lockRefreshGate(ctx, &state.gate, true)
	if gateErr != nil {
		return gateErr
	}
	defer state.gate.Unlock()

	if state.closed {
		return errServiceClosed
	}

	state.cancel()
	state.workers.Wait()

	defer service.restartRefreshGeneration()

	err := ctx.Err()
	if err == nil {
		err = reset()
	}

	if err == nil {
		for _, getter := range service.registry.All() {
			if resetter, ok := getter.(interface{ resetMetadata() }); ok {
				resetter.resetMetadata()
			}
		}

		service.mu.Lock()
		clear(service.failures)
		clear(service.lastFail)
		clear(service.lastErrors)
		clear(service.breakers)
		service.mu.Unlock()
	}

	return err
}

func cachedQuote(entry *models.PriceCacheEntry, stale bool) models.Price {
	quote := models.Price{
		Value: entry.Price, Currency: entry.Currency, Source: entry.Source,
		FetchedAt: entry.FetchedAt, IsStale: stale, IsFailed: entry.ErrorCount > 0,
	}
	if entry.LastError != "" {
		quote.Error = fmt.Errorf("%w: %s", errRefresh, entry.LastError)
	}

	return quote
}

func (service *Service) coalescedPrice(
	ctx context.Context,
	provider, instrument string,
	force, backgroundOnly bool,
) (models.Price, error) {
	ctx = refreshMetadataContext(ctx, force)
	instrument = safeQuoteInstrument(provider, instrument)

	requestErr := ctx.Err()
	if requestErr != nil {
		return models.Price{}, wrapOperationError("getPrice", requestErr)
	}

	getter, ok := service.registry.Get(provider)
	if !ok {
		return models.Price{}, fmt.Errorf("%w %s not found", errProvider, provider)
	}

	state := &service.refresh

	gateErr := lockRefreshGate(ctx, &state.gate, false)
	if gateErr != nil {
		return models.Price{}, gateErr
	}

	if state.closed {
		state.gate.RUnlock()

		return models.Price{}, errServiceClosed
	}

	cached, cacheErr := service.getCachedPriceContext(ctx, provider, instrument)
	if cacheErr != nil {
		cached = nil
	}

	contextErr := ctx.Err()
	if contextErr != nil {
		state.gate.RUnlock()

		return models.Price{}, wrapOperationError("getPrice", contextErr)
	}

	if canUseCachedQuote(cached, force) {
		state.gate.RUnlock()

		return cachedQuote(cached, false), nil
	}

	if service.breakerBlocksRequest(provider) {
		state.gate.RUnlock()

		err := fmt.Errorf("%w %s", errCircuitBreakerOpenFor, provider)

		if cached != nil {
			quote := cachedQuote(cached, true)
			quote.IsFailed = true
			quote.Error = err

			return quote, nil
		}

		return models.Price{}, err
	}

	background := backgroundOnly || (cached != nil && !force)
	flight, waiter, err := service.joinFlight(ctx, getter, instrument, cached, background)

	state.gate.RUnlock()

	if background {
		return refreshingQuote(cached, err)
	}

	if err != nil {
		return models.Price{}, err
	}

	return service.waitForFlight(ctx, flight, waiter)
}

// A cold background request has no quote yet; its shared flight owns the refresh.
func refreshingQuote(cached *models.PriceCacheEntry, err error) (models.Price, error) {
	if cached == nil {
		return models.Price{}, err
	}

	quote := cachedQuote(cached, true)
	if err != nil {
		quote.IsFailed = true
		quote.Error = err
	}

	return quote, nil
}

func (service *Service) waitForFlight(
	ctx context.Context,
	flight *priceFlight,
	waiter uint64,
) (models.Price, error) {
	select {
	case <-ctx.Done():
		service.leaveFlight(flight, waiter)

		return models.Price{}, wrapOperationError("getPrice", ctx.Err())
	case <-flight.done:
		contextErr := ctx.Err()
		if contextErr != nil {
			return models.Price{}, wrapOperationError("getPrice", contextErr)
		}

		return flight.quote, flight.err
	}
}

func (service *Service) tryJoinFlight(
	ctx context.Context,
	getter Getter,
	instrument string,
	cached *models.PriceCacheEntry,
	background bool,
) (*priceFlight, uint64, error) {
	state := &service.refresh
	state.mu.Lock()
	defer state.mu.Unlock()

	key := refreshKey{provider: getter.Name(), instrument: instrument}

	flight := state.flights[key]
	if flight != nil && (flight.ctx.Err() != nil || needsForcedFlight(ctx, flight.ctx)) {
		return flight, 0, errAbandonedFlight
	}

	if flight == nil {
		if len(state.flights)+state.probes >= maxPendingFetches {
			return nil, 0, errRefreshQueueFull
		}

		flightCtx, cancel := context.WithTimeout(state.ctx, refreshTimeout)

		flightCtx = context.WithValue(flightCtx, quoteGenerationKey{}, quoteGeneration(ctx))
		if minimum, ok := ctx.Value(metadataRefreshKey{}).(time.Time); ok {
			flightCtx = context.WithValue(flightCtx, metadataRefreshKey{}, minimum)
		}

		flight = &priceFlight{
			ctx:     flightCtx,
			cancel:  cancel,
			done:    make(chan struct{}),
			waiters: make(map[uint64]context.Context),
		}

		state.flights[key] = flight
		if state.providers[key.provider] == nil {
			state.providers[key.provider] = make(chan struct{}, 1)
		}

		state.workers.Add(1)
		go service.runFlight(key, flight, getter, cached, state.providers[key.provider])
	}

	flight.background = flight.background || background
	state.nextWaiter++

	waiter := state.nextWaiter
	if !background {
		flight.waiters[waiter] = ctx //nolint:fatcontext // Retain independent caller lifetimes.
	}

	return flight, waiter, nil
}

func (service *Service) leaveFlight(flight *priceFlight, waiter uint64) {
	state := &service.refresh
	state.mu.Lock()
	delete(flight.waiters, waiter)

	abandoned := !flight.background && len(flight.waiters) == 0
	if abandoned {
		flight.cancel()
	}
	state.mu.Unlock()
	// Join abandoned writes, so canceled callers cannot race subsequent reset/read.
	if abandoned {
		<-flight.done
	}
}

func (service *Service) flightLive(flight *priceFlight) bool {
	state := &service.refresh
	state.mu.Lock()
	defer state.mu.Unlock()

	if flight.ctx.Err() != nil {
		return false
	}

	if flight.background {
		return true
	}

	for _, ctx := range flight.waiters {
		if ctx.Err() == nil {
			return true
		}
	}

	flight.cancel()

	return false
}

func acquireSlot(ctx context.Context, slot chan struct{}) bool {
	select {
	case slot <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (service *Service) runFlight(
	key refreshKey,
	flight *priceFlight,
	getter Getter,
	cached *models.PriceCacheEntry,
	providerSlot chan struct{},
) {
	state := &service.refresh
	defer state.workers.Done()
	defer func() {
		flight.cancel()
		state.mu.Lock()
		delete(state.flights, key)
		close(flight.done)
		state.mu.Unlock()
	}()

	if !acquireSlot(flight.ctx, providerSlot) {
		flight.err = flight.ctx.Err()

		return
	}

	defer func() { <-providerSlot }()

	if !acquireSlot(flight.ctx, state.slots) {
		flight.err = flight.ctx.Err()

		return
	}

	defer func() { <-state.slots }()

	if !service.flightLive(flight) {
		flight.err = context.Canceled

		return
	}

	if service.breakerBlocksRequest(key.provider) {
		flight.err = fmt.Errorf("%w %s", errCircuitBreakerOpenFor, key.provider)
	} else {
		flight.quote, flight.err = fetchQuoteSafely(flight.ctx, getter, key.instrument)
		if !service.flightLive(flight) {
			flight.err = context.Canceled

			return
		}

		if flight.err == nil {
			flight.err = service.storeFetchedQuote(
				flight.ctx,
				getter,
				key.instrument,
				&flight.quote,
				true,
			)

			return
		}

		service.recordFailureContext(flight.ctx, key.provider, key.instrument, flight.err)
	}

	if cached != nil && flight.ctx.Err() == nil {
		quote := cachedQuote(cached, true)
		quote.IsFailed = true
		quote.Error = flight.err
		flight.quote, flight.err = quote, nil
	}
}

func (service *Service) refreshHoldings(ctx context.Context, holdings []models.Holding) error {
	ctx = context.WithValue(ctx, metadataRefreshKey{}, time.Now().UTC())
	keys := make([]refreshKey, 0, len(holdings))
	seen := make(map[refreshKey]bool)

	for index := range holdings {
		holding := &holdings[index]

		key := refreshKey{provider: holding.PriceProvider, instrument: holding.InstrumentID}
		if key.provider == "" || key.provider == manualProviderName || key.instrument == "" ||
			seen[key] {
			continue
		}

		seen[key] = true
		keys = append(keys, key)
	}

	failures := make([]error, len(keys))
	quotes := make([]models.Price, len(keys))

	var workers sync.WaitGroup
	for worker := range min(maxConcurrentFetches, len(keys)) {
		workers.Add(1)
		go func(offset int) {
			defer workers.Done()

			for index := offset; index < len(keys); index += maxConcurrentFetches {
				if ctx.Err() != nil {
					return
				}

				key := keys[index]

				quote, err := service.getPrice(ctx, key.provider, key.instrument, true)
				quotes[index] = quote

				if err == nil && (quote.IsFailed || quote.IsStale) {
					err = quote.Error
					if err == nil {
						err = errRefresh
					}
				}

				if err != nil {
					failures[index] = fmt.Errorf(
						"refresh %s/%s: %w",
						key.provider,
						key.instrument,
						err,
					)
				}
			}
		}(worker)
	}

	workers.Wait()

	return errors.Join(
		errors.Join(failures...),
		service.refreshHoldingsFX(ctx, holdings, quotes),
		ctx.Err(),
	)
}

// Polling TryLock keeps admission cancellation-aware without spawning waiter goroutines.
func lockRefreshGate(ctx context.Context, gate *sync.RWMutex, exclusive bool) error {
	timer := time.NewTicker(time.Millisecond)
	defer timer.Stop()

	for {
		contextErr := ctx.Err()
		if contextErr != nil {
			return wrapOperationError("refresh gate", contextErr)
		}

		if exclusive {
			if gate.TryLock() {
				return nil
			}
		} else if gate.TryRLock() {
			return nil
		}

		select {
		case <-ctx.Done():
			return wrapOperationError("refresh", ctx.Err())
		case <-timer.C:
		}
	}
}

var errProviderPanicked = errors.New("price provider panicked")

//nolint:nonamedreturns // Recovery must replace the result returned to every waiter.
func fetchQuoteSafely(
	ctx context.Context,
	getter Getter,
	instrument string,
) (quote models.Price, err error) {
	defer func() {
		if recover() != nil {
			quote = models.Price{}
			err = errProviderPanicked
		}
	}()

	quote, err = getter.GetPrice(ctx, instrument)

	return quote, wrapOperationError("fetchQuoteSafely", err)
}

var errAbandonedFlight = errors.New("previous quote fetch was canceled")

func (service *Service) joinFlight(
	ctx context.Context,
	getter Getter,
	instrument string,
	cached *models.PriceCacheEntry,
	background bool,
) (*priceFlight, uint64, error) {
	for {
		flight, waiter, err := service.tryJoinFlight(ctx, getter, instrument, cached, background)
		if !errors.Is(err, errAbandonedFlight) || background {
			return flight, waiter, err
		}

		select {
		case <-ctx.Done():
			return nil, 0, wrapOperationError("joinFlight", ctx.Err())
		case <-flight.done:
		}
	}
}

func (service *Service) restartRefreshGeneration() {
	nextCtx, cancel := context.WithCancel(context.Background())
	service.refresh.ctx, service.refresh.cancel = nextCtx, cancel //nolint:fatcontext // Renew service lifetime.
}

// Test requests share provider/global admission and service shutdown, but do not
// read/write the quote cache or change provider-health state.
func (service *Service) testProviderBounded(
	ctx context.Context,
	provider, instrument string,
) (models.Price, error) {
	getter, ok := service.registry.Get(provider)
	if !ok {
		return models.Price{}, fmt.Errorf("%w %s not found", errProvider, provider)
	}

	state := &service.refresh

	err := lockRefreshGate(ctx, &state.gate, false)
	if err != nil {
		return models.Price{}, err
	}

	if state.closed {
		state.gate.RUnlock()

		return models.Price{}, errServiceClosed
	}

	fetchCtx, cancel := context.WithTimeout(ctx, refreshTimeout)

	// The second lifetime intentionally belongs to the service, not the caller.
	//nolint:contextcheck // Close and reset must cancel the probe as well.
	stopCancel := context.AfterFunc(state.ctx, cancel)
	defer stopCancel()
	defer cancel()

	state.mu.Lock()
	if len(state.flights)+state.probes >= maxPendingFetches {
		state.mu.Unlock()
		state.gate.RUnlock()

		return models.Price{}, errRefreshQueueFull
	}

	state.probes++

	slot := state.providers[provider]
	if slot == nil {
		slot = make(chan struct{}, 1)
		state.providers[provider] = slot
	}
	state.mu.Unlock()
	state.workers.Add(1)

	state.gate.RUnlock()
	defer func() {
		state.mu.Lock()
		state.probes--
		state.mu.Unlock()
		state.workers.Done()
	}()

	if !acquireSlot(fetchCtx, slot) {
		return models.Price{}, wrapOperationError("TestProvider", fetchCtx.Err())
	}

	defer func() { <-slot }()

	if !acquireSlot(fetchCtx, state.slots) {
		return models.Price{}, wrapOperationError("TestProvider", fetchCtx.Err())
	}

	defer func() { <-state.slots }()

	quote, err := fetchQuoteSafely(fetchCtx, getter, instrument)

	if ctx.Err() != nil {
		return models.Price{}, wrapOperationError("TestProvider", ctx.Err())
	}

	if fetchCtx.Err() != nil {
		return models.Price{}, wrapOperationError("TestProvider", fetchCtx.Err())
	}

	return quote, err
}

func (service *Service) refreshHoldingsFX(
	ctx context.Context,
	holdings []models.Holding,
	quotes []models.Price,
) error {
	currency, err := db.GetSettingContext(ctx, service.db, "display_currency")
	if err != nil {
		return wrapOperationError("refreshHoldingsFX", err)
	}

	needsUSD, needsEUR := false, currency == currencyEUR

	for index := range holdings {
		holding := &holdings[index]
		needsUSD = needsUSD || holding.BaseCurrency != ""
		needsEUR = needsEUR || holding.BaseCurrency == currencyEUR ||
			holding.CurrentCurrency == currencyEUR
	}

	for index := range quotes {
		needsEUR = needsEUR || quotes[index].Currency == currencyEUR
	}

	var usdErr, eurErr error
	if needsUSD {
		_, usdErr = service.getUSDTTRYQuote(ctx, true)
	}

	if needsEUR {
		_, eurErr = service.getFXTRYQuote(ctx, currencyEUR, true)
	}

	return errors.Join(usdErr, eurErr)
}

func refreshMetadataContext(ctx context.Context, force bool) context.Context {
	if force && ctx.Value(metadataRefreshKey{}) == nil {
		return context.WithValue(ctx, metadataRefreshKey{}, time.Now().UTC())
	}

	return ctx
}

func needsForcedFlight(requestCtx, flightCtx context.Context) bool {
	return (quoteGeneration(requestCtx) >= 0 && quoteGeneration(requestCtx) != quoteGeneration(flightCtx)) ||
		(requestCtx.Value(metadataRefreshKey{}) != nil && flightCtx.Value(metadataRefreshKey{}) == nil)
}
