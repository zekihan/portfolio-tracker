package price

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

func TestQuoteCacheLifetimeUsesFetchAge(t *testing.T) {
	t.Parallel()
	service := newRefreshTestService(t, NewManual())

	now := time.Now().UTC().Truncate(time.Second)
	for _, age := range []time.Duration{14 * time.Minute, 16 * time.Minute, -time.Hour} {
		quote := models.Price{Value: 42, Currency: "TRY", FetchedAt: now.Add(-age)}

		err := service.cachePrice("Manual", age.String(), &quote, providerTTL)
		if err != nil {
			t.Fatal(err)
		}

		cached, err := service.getCachedPrice("Manual", age.String())
		if err != nil {
			t.Fatal(err)
		}

		want := quote.FetchedAt.Add(providerTTL)
		if age < 0 {
			want = now.Add(providerTTL)
		}

		if cached.ExpiresAt.Before(want) || cached.ExpiresAt.After(want.Add(time.Second)) {
			t.Fatalf("age %s expiry = %v, want %v", age, cached.ExpiresAt, want)
		}

		if age > providerTTL && canUseCachedQuote(cached, false) {
			t.Fatal("expired metadata became fresh")
		}
	}

	quote := models.Price{Value: 42, Currency: "TRY"}

	err := service.cachePrice("Manual", "missing-time", &quote, providerTTL)
	if err != nil {
		t.Fatal(err)
	}

	if quote.FetchedAt.IsZero() {
		t.Fatal("missing timestamp not initialized")
	}
}

type oldQuoteGetter struct{ baseGetter }

func (getter *oldQuoteGetter) GetPrice(context.Context, string) (models.Price, error) {
	return models.Price{Value: 42, Currency: "TRY", FetchedAt: time.Now().Add(-time.Hour)}, nil
}

func TestExpiredFetchedQuoteIsNeverReportedFresh(t *testing.T) {
	t.Parallel()

	getter := &oldQuoteGetter{baseGetter{name: "Old", ttl: providerTTL}}
	service := newRefreshTestService(t, getter)

	quote, err := service.GetPrice(t.Context(), getter.Name(), "OLD")
	if err != nil || !quote.IsStale {
		t.Fatalf("expired result = %+v, %v", quote, err)
	}

	err = service.RefreshAll(
		t.Context(),
		[]models.Holding{{PriceProvider: getter.Name(), InstrumentID: "OLD"}},
	)
	if err == nil {
		t.Fatal("refresh accepted expired metadata")
	}
}

func TestProbeQueueSharesBoundWithQuotes(t *testing.T) {
	t.Parallel()

	getter := newGatedGetter("Gated")
	service := newRefreshTestService(t, getter)
	seedStaleQuote(t, service, getter.Name())

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var workers sync.WaitGroup
	for range maxPendingFetches {
		workers.Go(func() {
			_, err := service.TestProvider(ctx, getter.Name(), "PROBE")
			if !errors.Is(err, context.Canceled) {
				t.Errorf("probe error = %v", err)
			}
		})
	}

	deadline := time.Now().Add(5 * time.Second)

	for {
		service.refresh.mu.Lock()
		probes := service.refresh.probes
		service.refresh.mu.Unlock()

		if probes == maxPendingFetches {
			break
		}

		if time.Now().After(deadline) {
			cancel()
			workers.Wait()
			t.Fatal("probe admission timed out")
		}

		<-time.After(time.Millisecond)
	}

	_, err := service.TestProvider(t.Context(), getter.Name(), "OVERFLOW")
	if !errors.Is(err, errRefreshQueueFull) {
		t.Fatalf("overflow probe error = %v", err)
	}

	_, err = service.GetPrice(t.Context(), getter.Name(), "COLD")
	if !errors.Is(err, errRefreshQueueFull) {
		t.Fatalf("overflow cold error = %v", err)
	}

	quote, err := service.GetPrice(t.Context(), getter.Name(), "TEST")
	if err != nil || !quote.IsStale || !quote.IsFailed ||
		!errors.Is(quote.Error, errRefreshQueueFull) {
		t.Fatalf("unscheduled stale quote = %+v, %v", quote, err)
	}

	cancel()
	workers.Wait()
	service.refresh.mu.Lock()
	probes := service.refresh.probes
	service.refresh.mu.Unlock()

	if probes != 0 {
		t.Fatalf("leaked probe reservations: %d", probes)
	}

	getter.release <- struct{}{}

	_, err = service.TestProvider(t.Context(), getter.Name(), "RECOVERED")
	if err != nil {
		t.Fatal(err)
	}
}

func TestAlphaCurrencyCapacityRetainsExistingEntries(t *testing.T) {
	t.Parallel()

	getter := NewAlphaVantage()

	getter.currencies = make(map[string]*sharedMetadata[string])
	for index := range maxPendingFetches {
		getter.currencies[strconv.Itoa(index)] = &sharedMetadata[string]{
			value: "USD", expires: time.Now().Add(time.Hour), generation: -1,
		}
	}

	currency, err := getter.quoteCurrency(t.Context(), "0", "unused")
	if err != nil || currency != "USD" {
		t.Fatalf("existing currency = %s, %v", currency, err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"bestMatches":[{"1. symbol":"NEW","8. currency":"TRY"}]}`)
	}))
	defer server.Close()

	setRegressionTransport(t, getter.client, server.URL)

	currency, err = getter.quoteCurrency(t.Context(), "NEW", "unused")
	if err != nil || currency != "TRY" {
		t.Fatalf("new currency = %s, %v", currency, err)
	}

	if len(getter.currencies) != maxPendingFetches {
		t.Fatalf("cache entries = %d", len(getter.currencies))
	}

	retained := 0

	for index := range maxPendingFetches {
		if getter.currencies[strconv.Itoa(index)] != nil {
			retained++
		}
	}

	if retained != maxPendingFetches-1 {
		t.Fatalf("retained = %d", retained)
	}
}

func TestColdProviderHealthRetainsSafeFailureAndClearsIt(t *testing.T) {
	t.Parallel()

	for _, reset := range []string{"success", "reset", "generation"} {
		t.Run(reset, func(t *testing.T) {
			t.Parallel()
			service := newRefreshTestService(t, NewFrankfurter())
			service.recordFailure(
				"Frankfurter",
				"USD",
				fmt.Errorf("secret upstream URL?apikey=secret: %w", errRateLimited),
			)

			health, err := service.GetProviderHealth()
			if err != nil {
				t.Fatal(err)
			}

			for _, provider := range health {
				if provider.Name == "Frankfurter" &&
					provider.ErrorMsg != "Provider rate limit reached" {
					t.Fatalf("cold failure message = %q", provider.ErrorMsg)
				}
			}

			switch reset {
			case "success":
				service.recordSuccess("Frankfurter")
			case "reset":
				service.ResetFailures()
			case "generation":
				service.observeGeneration(1)
			}

			service.mu.RLock()
			message := service.lastErrors["Frankfurter"]
			service.mu.RUnlock()

			if message != "" {
				t.Fatalf("old diagnostic survived %s: %q", reset, message)
			}
		})
	}
}

func TestSupersededFailureCannotRestoreHealthDiagnostic(t *testing.T) {
	t.Parallel()
	service := newRefreshTestService(t, NewFrankfurter())

	ctx, err := service.withQuoteGeneration(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.db.ExecContext(
		t.Context(),
		"UPDATE snapshot_state SET generation = generation + 1 WHERE id = 1",
	)
	if err != nil {
		t.Fatal(err)
	}

	service.recordFailureContext(ctx, "Frankfurter", "USD", errRateLimited)
	service.mu.RLock()
	diagnostic := service.lastErrors["Frankfurter"]
	service.mu.RUnlock()

	if diagnostic != "" {
		t.Fatalf("superseded diagnostic = %q", diagnostic)
	}
}

func TestHealthDiagnosticDoesNotExposeUnknownProviderText(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("https://provider.invalid?apikey=secret: %w", errProviderUnavailable)
	if got := safeHealthError(err); got != "Provider request failed" {
		t.Fatalf("unsafe diagnostic %q", got)
	}
}

func TestProviderMappingReadsNullableLegacyHolding(t *testing.T) {
	t.Parallel()
	service := newRefreshTestService(t)

	_, err := service.db.ExecContext(
		t.Context(),
		`INSERT INTO holdings (name, asset_type, price_provider, instrument_id)
 VALUES ('Legacy', 'Cash', NULL, NULL), ('Quoted', 'Stock', 'Yahoo Finance', 'TEST')`,
	)
	if err != nil {
		t.Fatal(err)
	}

	mappings, err := service.GetHoldingProviderMappingContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if len(mappings) != 2 || mappings[0].HoldingName != "Legacy" ||
		mappings[0].Provider != "" || mappings[0].InstrumentID != "" ||
		mappings[1].Provider != "Yahoo Finance" || mappings[1].InstrumentID != "TEST" {
		t.Fatalf("legacy mappings = %+v", mappings)
	}
}
