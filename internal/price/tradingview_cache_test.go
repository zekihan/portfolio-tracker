package price

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

func TestTradingViewSafeQuoteInstrument(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ provider, input, want string }{
		{"TradingView Crypto", "TUSD-USD", "CRYPTO:TUSDUSD"},
		{"TradingView Crypto", " tusd-usd ", "CRYPTO:TUSDUSD"},
		{"TradingView Crypto", "CRYPTO:TUSD-USD", "CRYPTO:TUSDUSD"},
		{"TradingView Crypto", "CURRENCY:TUSD-USD", "CRYPTO:TUSDUSD"},
		{"TradingView Crypto", "PYUSD-USD", "CRYPTO:PYUSDUSD"},
		{"TradingView Crypto", "FDUSD-USD", "CRYPTO:FDUSDUSD"},
		{"TradingView Crypto", "CRYPTO:TUSDUSD", "CRYPTO:TUSDUSD"},
		{"TradingView Crypto", "TUSD", "TUSD"},
		{"TradingView Crypto", "ETH-USD", "ETH-USD"},
		{"TradingView Crypto", "BTC", "BTC"},
		{"Yahoo Finance", "TUSD-USD", "TUSD-USD"},
	} {
		if got := safeQuoteInstrument(test.provider, test.input); got != test.want {
			t.Errorf("%s/%q = %q, want %q", test.provider, test.input, got, test.want)
		}
	}
}

func TestTradingViewBypassesUnsafeLegacyCache(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"TUSD-USD", " tusd-usd ", "CRYPTO:TUSD-USD", "CURRENCY:TUSD-USD"} {
		for _, age := range []time.Duration{0, time.Hour} {
			for _, force := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/force=%t", input, age, force), func(t *testing.T) {
					t.Parallel()
					getter, fail, calls := tradingViewCacheGetter(t)
					fail.Store(true)

					service := newRefreshTestService(t, getter)
					quote := models.Price{
						Value:     0.005,
						Currency:  "USD",
						Source:    getter.Name(),
						FetchedAt: time.Now().Add(-age),
					}

					err := service.cachePrice(
						getter.Name(),
						input,
						&quote,
						providerTTL,
					)
					if err != nil {
						t.Fatal(err)
					}

					got, err := service.getPrice(t.Context(), getter.Name(), input, force)
					if err == nil || got.Value != 0 || calls.Load() != 1 {
						t.Fatalf(
							"unsafe legacy quote escaped: %+v, %v, calls=%d",
							got,
							err,
							calls.Load(),
						)
					}
				})
			}
		}
	}
}

func TestTradingViewCorrectedCacheRefreshAndFallback(t *testing.T) {
	t.Parallel()
	getter, fail, calls := tradingViewCacheGetter(t)
	service := newRefreshTestService(t, getter)
	old := models.Price{Value: 0.005, Currency: "USD", Source: getter.Name(), FetchedAt: time.Now()}

	err := service.cachePrice(getter.Name(), "TUSD-USD", &old, providerTTL)
	if err != nil {
		t.Fatal(err)
	}

	holdings := []models.Holding{{PriceProvider: getter.Name(), InstrumentID: "TUSD-USD"}}

	err = service.RefreshAll(t.Context(), holdings)
	if err != nil {
		t.Fatal(err)
	}

	assertTradingViewCachedAliases(t, service)

	if calls.Load() != 1 {
		t.Fatalf("correct aliases fetched %d times", calls.Load())
	}

	fail.Store(true)

	correct := models.Price{
		Value: 0.999, Currency: "USD", Source: getter.Name(), FetchedAt: time.Now().Add(-time.Hour),
	}

	err = service.cachePrice(getter.Name(), "CRYPTO:TUSDUSD", &correct, providerTTL)
	if err != nil {
		t.Fatal(err)
	}

	quote, err := service.GetPrice(t.Context(), getter.Name(), "TUSD-USD")
	if err != nil || quote.Value != 0.999 || !quote.IsStale {
		t.Fatalf("safe background fallback = %+v, %v", quote, err)
	}

	err = service.RefreshAll(t.Context(), holdings)
	if err == nil {
		t.Fatal("failed refresh reported success")
	}

	quote, err = service.getPrice(t.Context(), getter.Name(), "TUSD-USD", true)
	if err != nil || quote.Value != 0.999 || !quote.IsStale || !quote.IsFailed {
		t.Fatalf("corrected fallback = %+v, %v", quote, err)
	}
}

func tradingViewCacheGetter(t *testing.T) (*TradingViewCrypto, *atomic.Bool, *atomic.Int32) {
	t.Helper()

	fail := &atomic.Bool{}
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		var payload struct {
			Symbols struct {
				Tickers []string `json:"tickers"`
			} `json:"symbols"`
		}

		err := json.NewDecoder(r.Body).Decode(&payload)
		if err != nil {
			t.Error(err)
		}

		if len(payload.Symbols.Tickers) != 1 || payload.Symbols.Tickers[0] != "CRYPTO:TUSDUSD" {
			t.Errorf("unsafe ticker: %v", payload.Symbols.Tickers)
		}

		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)

			return
		}

		fmt.Fprint(w, `{"data":[{"s":"CRYPTO:TUSDUSD","d":[0.999,"USD"]}]}`)
	}))
	t.Cleanup(server.Close)

	getter := NewTradingViewCrypto()
	setRegressionTransport(t, getter.client, server.URL)

	return getter, fail, calls
}

func assertTradingViewCachedAliases(t *testing.T, service *Service) {
	t.Helper()

	for _, input := range []string{"TUSD-USD", " tusd-usd ", "CRYPTO:TUSD-USD", "CURRENCY:TUSD-USD", "CRYPTO:TUSDUSD"} {
		quote, quoteErr := service.GetPrice(t.Context(), "TradingView Crypto", input)
		if quoteErr != nil || quote.Value != 0.999 || quote.IsStale || quote.IsFailed {
			t.Fatalf("corrected cache lookup %q = %+v, %v", input, quote, quoteErr)
		}
	}
}
