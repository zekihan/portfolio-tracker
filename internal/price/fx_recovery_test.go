package price

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

type recoveryFXGetter struct {
	*gatedQuoteGetter

	failure error
}

func (getter *recoveryFXGetter) GetPrice(
	ctx context.Context,
	instrument string,
) (models.Price, error) {
	quote, err := getter.gatedQuoteGetter.GetPrice(ctx, instrument)
	if err != nil {
		return models.Price{}, err
	}

	if getter.failure != nil {
		return models.Price{}, getter.failure
	}

	quote.Value, quote.Currency = 40, "TRY"

	return quote, nil
}

func seedRecoveryFX(
	t *testing.T,
	service *Service,
	provider, instrument string,
	value float64,
	age time.Duration,
) {
	t.Helper()

	quote := models.Price{
		Value:     value,
		Currency:  "TRY",
		Source:    provider,
		FetchedAt: time.Now().Add(-age),
	}

	err := service.cachePrice(provider, instrument, &quote, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
}

func outageRecoveryService(t *testing.T, fallback Getter) *Service {
	t.Helper()

	primary := &regressionFXGetter{name: "Frankfurter", ttl: time.Minute, fail: true}
	service := newRefreshTestService(t, primary, fallback)
	seedRecoveryFX(t, service, primary.Name(), "USD", 30, 72*time.Hour)

	for range breakerFailureThreshold {
		service.recordFailure(primary.Name(), "USD", errProviderUnavailable)
	}

	return service
}

func recoveryFlight(t *testing.T, service *Service) *priceFlight {
	t.Helper()
	service.refresh.mu.Lock()
	flight := service.refresh.flights[refreshKey{provider: "Yahoo Finance", instrument: "USDTRY=X"}]
	service.refresh.mu.Unlock()

	if flight == nil {
		t.Fatal("fallback refresh was not admitted")
	}

	return flight
}

func TestFXRecoversThroughCoalescedBackgroundFallback(t *testing.T) {
	t.Parallel()

	for _, cached := range []bool{false, true} {
		name := "cold fallback"
		if cached {
			name = "expired fallback"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkFXRecovery(t, cached)
		})
	}
}

func checkFXRecovery(t *testing.T, cached bool) {
	t.Helper()

	fallback := &recoveryFXGetter{gatedQuoteGetter: newGatedGetter("Yahoo Finance")}

	service := outageRecoveryService(t, fallback)
	if cached {
		seedRecoveryFX(t, service, fallback.Name(), "USDTRY=X", 20, 96*time.Hour)
	}

	result := make(chan models.Price, 1)

	go func() {
		quote, err := service.GetUSDTTRYQuote(t.Context())
		if err != nil {
			t.Error(err)
		}

		result <- quote
	}()
	// The read must finish while the fallback provider is still blocked.
	first := awaitRefreshSignal(t, result)
	if first.Value != 30 || !first.IsStale || !first.IsFailed {
		t.Fatalf("initial FX=%+v", first)
	}

	awaitRefreshSignal(t, fallback.entered)

	flight := recoveryFlight(t, service)
	for range 8 {
		quote, err := service.GetUSDTTRYQuote(t.Context())
		if err != nil || quote.Value != 30 {
			t.Fatalf("pending FX=%+v, %v", quote, err)
		}
	}

	if fallback.calls.Load() != 1 {
		t.Fatalf("duplicate fallback requests=%d", fallback.calls.Load())
	}

	fallback.release <- struct{}{}

	awaitRefreshSignal(t, flight.done)

	checkRecoveredFX(t, service, fallback)
}

func checkRecoveredFX(t *testing.T, service *Service, fallback *recoveryFXGetter) {
	t.Helper()

	quote, err := service.GetUSDTTRYQuote(t.Context())
	if err != nil || quote.Value != 40 || quote.IsStale || quote.IsFailed ||
		quote.Source != fallback.Name() {
		t.Fatalf("recovered FX=%+v, %v", quote, err)
	}

	if fallback.calls.Load() != 1 {
		t.Fatal("fresh fallback bypassed its cache")
	}
}

func TestFXFallbackFailurePreservesNewestStaleSample(t *testing.T) {
	t.Parallel()

	fallback := &recoveryFXGetter{
		gatedQuoteGetter: newGatedGetter("Yahoo Finance"),
		failure:          errProviderUnavailable,
	}
	service := outageRecoveryService(t, fallback)
	seedRecoveryFX(t, service, fallback.Name(), "USDTRY=X", 35, time.Hour)

	quote, err := service.GetUSDTTRYQuote(t.Context())
	if err != nil || quote.Value != 35 || !quote.IsStale {
		t.Fatalf("cached fallback=%+v, %v", quote, err)
	}

	awaitRefreshSignal(t, fallback.entered)
	flight := recoveryFlight(t, service)

	fallback.release <- struct{}{}

	awaitRefreshSignal(t, flight.done)

	quote, err = service.GetUSDTTRYQuote(t.Context())
	if err != nil || quote.Value != 35 || !quote.IsStale || !quote.IsFailed || quote.Error == nil {
		t.Fatalf("failed fallback=%+v, %v", quote, err)
	}
}

func TestFXBackgroundFallbackHonorsCancellationAndClose(t *testing.T) {
	t.Parallel()

	fallback := &recoveryFXGetter{gatedQuoteGetter: newGatedGetter("Yahoo Finance")}
	service := outageRecoveryService(t, fallback)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := service.GetUSDTTRYQuote(ctx)
	if !errors.Is(err, context.Canceled) || fallback.calls.Load() != 0 {
		t.Fatalf("canceled FX started fallback: %v, calls=%d", err, fallback.calls.Load())
	}

	quote, err := service.GetUSDTTRYQuote(t.Context())
	if err != nil || quote.Value != 30 {
		t.Fatalf("stale FX=%+v, %v", quote, err)
	}

	awaitRefreshSignal(t, fallback.entered)
	flight := recoveryFlight(t, service)

	err = service.Close()
	if err != nil {
		t.Fatal(err)
	}

	awaitRefreshSignal(t, flight.done)

	if !errors.Is(flight.err, context.Canceled) {
		t.Fatalf("closed fallback error=%v", flight.err)
	}

	cached, err := service.getCachedPrice(fallback.Name(), "USDTRY=X")
	if err != nil || cached != nil {
		t.Fatalf("canceled fallback persisted=%+v, %v", cached, err)
	}

	service.mu.RLock()
	failures := service.failures[fallback.Name()]
	service.mu.RUnlock()

	if failures != 0 {
		t.Fatalf("cancellation recorded %d provider failures", failures)
	}
}

func TestFXHealthyPrimaryDoesNotSpendFallbackQuota(t *testing.T) {
	t.Parallel()

	primary := &regressionFXGetter{name: "Frankfurter", ttl: time.Minute}
	fallback := &recoveryFXGetter{gatedQuoteGetter: newGatedGetter("Yahoo Finance")}
	service := newRefreshTestService(t, primary, fallback)
	seedRecoveryFX(t, service, primary.Name(), "USD", 40, 0)

	for range 8 {
		quote, err := service.GetUSDTTRYQuote(t.Context())
		if err != nil || quote.Value != 40 || quote.IsStale || quote.IsFailed {
			t.Fatalf("healthy FX=%+v, %v", quote, err)
		}
	}

	service.refresh.mu.Lock()
	pending := len(service.refresh.flights)
	service.refresh.mu.Unlock()

	if pending != 0 || fallback.calls.Load() != 0 {
		t.Fatal("healthy primary caused an unnecessary fallback refresh")
	}
}

func TestFXColdFallbackFailuresRespectCircuitBreaker(t *testing.T) {
	t.Parallel()

	fallback := &recoveryFXGetter{
		gatedQuoteGetter: newGatedGetter("Yahoo Finance"),
		failure:          errProviderUnavailable,
	}
	service := outageRecoveryService(t, fallback)

	for range breakerFailureThreshold {
		quote, err := service.GetUSDTTRYQuote(t.Context())
		if err != nil || quote.Value != 30 || !quote.IsStale || !quote.IsFailed {
			t.Fatalf("fallback failure lost primary sample: %+v, %v", quote, err)
		}

		awaitRefreshSignal(t, fallback.entered)
		flight := recoveryFlight(t, service)

		fallback.release <- struct{}{}

		awaitRefreshSignal(t, flight.done)
	}

	quote, err := service.GetUSDTTRYQuote(t.Context())
	if err != nil || quote.Value != 30 || !quote.IsStale || !quote.IsFailed {
		t.Fatalf("open breaker lost primary sample: %+v, %v", quote, err)
	}

	if fallback.calls.Load() != breakerFailureThreshold {
		t.Fatal("fallback ignored its provider circuit breaker")
	}

	cached, err := service.getCachedPrice(fallback.Name(), "USDTRY=X")
	if err != nil || cached != nil {
		t.Fatalf("failed cold fallback cached a quote: %+v, %v", cached, err)
	}
}
