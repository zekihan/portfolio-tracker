package price

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

func TestEURTRYQuoteUsesIndependentFifteenMinuteCache(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			calls.Add(1)

			if request.URL.Query().Get("to") != "TRY" {
				t.Errorf("unexpected FX target: %s", request.URL)
			}

			w.Header().Set("Content-Type", "application/json")

			switch request.URL.Query().Get("from") {
			case "EUR":
				_, _ = w.Write([]byte(`{"rates":{"TRY":50}}`))
			case "USD":
				_, _ = w.Write([]byte(`{"rates":{"TRY":40}}`))
			default:
				t.Errorf("unexpected FX base: %s", request.URL)
				w.WriteHeader(http.StatusBadRequest)
			}
		}),
	)
	t.Cleanup(server.Close)

	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	getter := NewFrankfurter()
	getter.client.Transport = &localProviderTransport{target: target}
	service := newRefreshTestService(t, getter)

	for range 3 {
		eur, quoteErr := service.GetEURTRYQuote(t.Context())
		checkFreshFXQuote(t, &eur, quoteErr, 50)

		usd, quoteErr := service.GetUSDTTRYQuote(t.Context())
		checkFreshFXQuote(t, &usd, quoteErr, 40)
	}

	if calls.Load() != 2 {
		t.Fatalf("FX requests = %d, want one per currency", calls.Load())
	}

	for _, currency := range []string{"EUR", "USD"} {
		cached, cacheErr := service.getCachedPrice(getter.Name(), currency)
		if cacheErr != nil || cached == nil {
			t.Fatalf("%s cache = %+v, %v", currency, cached, cacheErr)
		}

		if lifetime := cached.ExpiresAt.Sub(cached.FetchedAt); lifetime != 15*time.Minute {
			t.Errorf("%s cache lifetime = %s", currency, lifetime)
		}
	}
}

func TestEURTRYQuoteFallsBackForUnusablePrimary(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		currency string
		value    float64
	}{
		{name: "zero", currency: "TRY", value: 0},
		{name: "negative", currency: "TRY", value: -1},
		{name: "infinite", currency: "TRY", value: math.Inf(1)},
		{name: "wrong currency", currency: "USD", value: 50},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			primary := &regressionFXGetter{name: "Frankfurter", ttl: time.Minute}
			fallback := &regressionFXGetter{name: "Yahoo Finance", ttl: time.Minute}
			service := newRefreshTestService(t, primary, fallback)
			quote := models.Price{
				Value:    test.value,
				Currency: test.currency,
				Source:   primary.Name(),
			}

			err := service.cachePrice(primary.Name(), "EUR", &quote, time.Minute)
			if err != nil {
				t.Fatal(err)
			}

			seedRecoveryFX(t, service, fallback.Name(), "EURTRY=X", 50, 0)
			seedRecoveryFX(t, service, fallback.Name(), "USDTRY=X", 40, 0)

			result, err := service.GetEURTRYQuote(t.Context())
			if err != nil || result.Value != 50 || result.Source != fallback.Name() ||
				result.Currency != "TRY" {
				t.Fatalf("EUR fallback = %+v, %v", result, err)
			}

			if primary.calls.Load() != 0 || fallback.calls.Load() != 0 {
				t.Fatal("FX fallback bypassed the fresh cache")
			}
		})
	}
}

func TestEURTRYQuoteProviderFailuresDoNotReturnUSDRate(t *testing.T) {
	t.Parallel()

	primary := &regressionFXGetter{name: "Frankfurter", ttl: time.Minute, fail: true}
	fallback := &regressionFXGetter{name: "Yahoo Finance", ttl: time.Minute, fail: true}
	service := newRefreshTestService(t, primary, fallback)
	seedRecoveryFX(t, service, primary.Name(), "USD", 40, 0)
	seedRecoveryFX(t, service, fallback.Name(), "USDTRY=X", 41, 0)

	quote, err := service.GetEURTRYQuote(t.Context())
	if err == nil || quote.Value != 0 {
		t.Fatalf("unavailable EUR/TRY = %+v, %v", quote, err)
	}

	usd, err := service.GetUSDTTRYQuote(t.Context())
	if err != nil || usd.Value != 40 {
		t.Fatalf("EUR failure affected USD/TRY = %+v, %v", usd, err)
	}
}

func TestEURTRYQuoteRecoversWithoutBlockingOnFallback(t *testing.T) {
	t.Parallel()

	primary := &regressionFXGetter{name: "Frankfurter", ttl: time.Minute, fail: true}
	fallback := &recoveryFXGetter{gatedQuoteGetter: newGatedGetter("Yahoo Finance")}
	service := newRefreshTestService(t, primary, fallback)
	seedRecoveryFX(t, service, primary.Name(), "EUR", 35, 72*time.Hour)

	for range breakerFailureThreshold {
		service.recordFailure(primary.Name(), "EUR", errProviderUnavailable)
	}

	for range 3 {
		quote, err := service.GetEURTRYQuote(t.Context())
		if err != nil || quote.Value != 35 || !quote.IsStale || !quote.IsFailed {
			t.Fatalf("stale EUR/TRY = %+v, %v", quote, err)
		}
	}

	if instrument := awaitRefreshSignal(t, fallback.entered); instrument != "EURTRY=X" {
		t.Fatalf("fallback instrument = %s", instrument)
	}

	service.refresh.mu.Lock()
	flight := service.refresh.flights[refreshKey{provider: fallback.Name(), instrument: "EURTRY=X"}]
	service.refresh.mu.Unlock()

	if flight == nil || fallback.calls.Load() != 1 {
		t.Fatalf("EUR fallback was not coalesced: calls=%d", fallback.calls.Load())
	}

	fallback.release <- struct{}{}

	awaitRefreshSignal(t, flight.done)

	quote, err := service.GetEURTRYQuote(t.Context())
	checkFreshFXQuote(t, &quote, err, 40)

	if quote.Source != fallback.Name() {
		t.Fatalf("recovered EUR/TRY source = %s", quote.Source)
	}
}

func TestEURTRYQuoteCancellationDoesNotFetchOrMutateCache(t *testing.T) {
	t.Parallel()

	primary := &recoveryFXGetter{gatedQuoteGetter: newGatedGetter("Frankfurter")}
	fallback := &regressionFXGetter{name: "Yahoo Finance", ttl: time.Minute}
	service := newRefreshTestService(t, primary, fallback)
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)

	go func() {
		_, err := service.GetEURTRYQuote(ctx)
		result <- err
	}()

	if instrument := awaitRefreshSignal(t, primary.entered); instrument != "EUR" {
		t.Fatalf("primary instrument = %s", instrument)
	}

	cancel()

	err := awaitRefreshSignal(t, result)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled EUR lookup = %v", err)
	}

	if fallback.calls.Load() != 0 {
		t.Fatal("canceled EUR lookup fetched its fallback")
	}

	cached, err := service.getCachedPrice(primary.Name(), "EUR")
	if err != nil || cached != nil {
		t.Fatalf("canceled EUR cache = %+v, %v", cached, err)
	}

	service.mu.RLock()
	failures := service.failures[primary.Name()]
	service.mu.RUnlock()

	if failures != 0 {
		t.Fatalf("cancellation recorded %d FX failures", failures)
	}
}

func checkFreshFXQuote(t *testing.T, quote *models.Price, err error, value float64) {
	t.Helper()

	if err != nil || quote.Value != value || quote.Currency != "TRY" || quote.IsStale ||
		quote.IsFailed {
		t.Fatalf("FX quote = %+v, %v; want fresh TRY quote %v", quote, err, value)
	}
}
