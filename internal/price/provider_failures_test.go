package price

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
)

func TestProviderFailureClassification(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		err        error
		name       string
		instrument bool
	}{
		{name: "missing ticker", err: fmt.Errorf("%w TEST", errNoDataFor), instrument: true},
		{name: "wrapped instrument", err: fmt.Errorf("fetch: %w", instrumentFailure(errInstrument)), instrument: true},
		{name: "bad request", err: providerHTTPError(errYahooFinanceReturnedHTTP, 400), instrument: true},
		{name: "not found", err: providerHTTPError(errYahooFinanceReturnedHTTP, 404), instrument: true},
		{name: "unprocessable", err: providerHTTPError(errAPIError, 422), instrument: true},
		{name: "unauthorized", err: providerHTTPError(errYahooFinanceReturnedHTTP, 401), instrument: false},
		{name: "forbidden", err: providerHTTPError(errYahooFinanceReturnedHTTP, 403), instrument: false},
		{name: "rate limited", err: providerHTTPError(errYahooFinanceReturnedHTTP, 429), instrument: false},
		{name: "outage", err: providerHTTPError(errYahooFinanceReturnedHTTP, 503), instrument: false},
		{name: "transport", err: io.ErrUnexpectedEOF, instrument: false},
		{name: "oversized", err: errProviderResponseTooLarge, instrument: false},
		{name: "unknown", err: errProviderUnavailable, instrument: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := isInstrumentFailure(test.err); got != test.instrument {
				t.Errorf("isInstrumentFailure(%v) = %v, want %v", test.err, got, test.instrument)
			}
		})
	}
}

type selectiveFailureGetter struct {
	failure error
	calls   int
}

func (getter *selectiveFailureGetter) Name() string       { return "Selective" }
func (getter *selectiveFailureGetter) Type() string       { return stockETFType }
func (getter *selectiveFailureGetter) TTL() time.Duration { return time.Minute }

func (getter *selectiveFailureGetter) GetPrice(
	_ context.Context,
	instrument string,
) (models.Price, error) {
	getter.calls++
	if instrument == "BAD" {
		return models.Price{}, getter.failure
	}

	return models.Price{
		Value: 25, Currency: "USD", Source: getter.Name(), FetchedAt: time.Now(),
	}, nil
}

func TestInstrumentFailureDoesNotPoisonSiblingCacheOrBreaker(t *testing.T) {
	t.Parallel()

	for _, failure := range []error{instrumentFailure(errInstrument), errProviderUnavailable} {
		t.Run(failure.Error(), func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			getter := &selectiveFailureGetter{failure: failure}
			registry := NewRegistry()
			registry.Register(getter)
			service := NewService(database, registry)

			seedAndFailInstrument(t, service, getter.Name())

			assertInstrumentCacheFailures(t, service, getter.Name())

			wantBlocked := !isInstrumentFailure(failure)
			if service.breakerBlocksRequest(getter.Name()) != wantBlocked {
				t.Fatal("incorrect breaker scope")
			}

			fresh, err := service.getPrice(t.Context(), getter.Name(), "GOOD", true)
			if err != nil {
				t.Fatal(err)
			}

			if wantBlocked {
				if !fresh.IsFailed || getter.calls != breakerFailureThreshold {
					t.Fatal("outage did not block sibling fetch")
				}
			} else if fresh.Value != 25 || fresh.IsFailed || getter.calls != breakerFailureThreshold+1 {
				t.Fatal("instrument failure prevented healthy sibling fetch")
			}
		})
	}
}

func TestReadProviderBodyLimit(t *testing.T) {
	t.Parallel()

	for _, size := range []int{maxProviderResponseBytes - 1, maxProviderResponseBytes, maxProviderResponseBytes + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			t.Parallel()

			body, err := readProviderBody(strings.NewReader(strings.Repeat(" ", size)))
			if size > maxProviderResponseBytes {
				if !errors.Is(err, errProviderResponseTooLarge) || body != nil {
					t.Fatalf("oversize accepted: length=%d err=%v", len(body), err)
				}
			} else if err != nil || len(body) != size {
				t.Fatalf("valid body rejected: length=%d err=%v", len(body), err)
			}
		})
	}
}

type oversizedBodyTransport struct{}

func (oversizedBodyTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	// A syntactically valid JSON prefix plus whitespace must not be silently truncated.
	return &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(
			strings.NewReader("{}" + strings.Repeat(" ", maxProviderResponseBytes)),
		),
	}, nil
}

func TestEveryNetworkProviderRejectsOversizedBody(t *testing.T) {
	t.Setenv("ALPHA_VANTAGE_KEY", "test")
	t.Setenv("TWELVE_DATA_KEY", "test")
	t.Setenv("FINNHUB_KEY", "test")

	for _, getter := range DefaultRegistry().All() {
		if getter.Name() == "Manual" {
			continue
		}

		t.Run(getter.Name(), func(t *testing.T) {
			client := providerTestClient(getter)
			client.Transport = oversizedBodyTransport{}

			_, err := getter.GetPrice(t.Context(), "TEST")
			if !errors.Is(err, errProviderResponseTooLarge) {
				t.Fatalf("error = %v, want oversized body rejection", err)
			}
		})
	}
}

type failureBodyTransport struct{ body string }

func (transport failureBodyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	body := transport.body
	if request.URL.Path == "/webservice/lastdate" {
		body = `"2026-10-12"`
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func TestProviderAPIErrorScope(t *testing.T) {
	t.Setenv("ALPHA_VANTAGE_KEY", "test")
	t.Setenv("TWELVE_DATA_KEY", "test")
	t.Setenv("FINNHUB_KEY", "test")

	for _, test := range []struct {
		name, provider, body string
		instrument           bool
	}{
		{
			name: "garanti missing data", provider: "Garanti BBVA",
			body: `{}`, instrument: false,
		},
		{
			name: "coingecko error envelope", provider: "CoinGecko",
			body: `{"status":{"error_code":429,"error_message":"Limit"}}`, instrument: false,
		},
		{
			name: "izko missing data", provider: "İZKO Gold",
			body: `{"success":true}`, instrument: false,
		},
		{
			name: "bist malformed row", provider: "TradingView BIST",
			body: `{"data":[{"s":"BIST:INVALID","d":[]}]}`, instrument: false,
		},
		{
			name: "alpha malformed quote", provider: "Alpha Vantage",
			body: `{"Global Quote":{"05. price":"oops"}}`, instrument: false,
		},
		{
			name: "alpha missing envelope", provider: "Alpha Vantage",
			body: `{}`, instrument: false,
		},
		{
			name: "twelve malformed quote", provider: "Twelve Data",
			body: `{"price":"oops"}`, instrument: false,
		},
		{
			name: "twelve missing quote", provider: "Twelve Data",
			body: `{}`, instrument: false,
		},
		{
			name: "finnhub missing quote", provider: "Finnhub",
			body: `{}`, instrument: false,
		},
		{
			name: "pusula missing envelope", provider: "Pusula Portföy",
			body: `{}`, instrument: false,
		},
		{
			name: "fx missing envelope", provider: "Frankfurter",
			body: `{}`, instrument: false,
		},
		{
			name: "bist missing envelope", provider: "TradingView BIST",
			body: `{}`, instrument: false,
		},
		{
			name: "yahoo missing envelope", provider: "Yahoo Finance",
			body: `{}`, instrument: false,
		},
		{
			name: "yahoo missing", provider: "Yahoo Finance",
			body: `{"chart":{"error":{"code":"Not Found"}}}`, instrument: true,
		},
		{
			name: "yahoo auth", provider: "Yahoo Finance",
			body: `{"chart":{"error":{"code":"Unauthorized"}}}`, instrument: false,
		},
		{
			name: "yahoo unknown", provider: "Yahoo Finance",
			body: `{"chart":{"error":{"code":"Unknown"}}}`, instrument: false,
		},
		{
			name: "alpha empty quote", provider: "Alpha Vantage",
			body: `{"Global Quote":{}}`, instrument: true,
		},
		{
			name: "alpha quota", provider: "Alpha Vantage",
			body: `{"Note":"Limit exceeded"}`, instrument: false,
		},
		{
			name: "alpha information", provider: "Alpha Vantage",
			body: `{"Information":"Access denied"}`, instrument: false,
		},
		{
			name: "twelve invalid symbol", provider: "Twelve Data",
			body: `{"code":400,"message":"Invalid symbol"}`, instrument: true,
		},
		{
			name: "twelve auth", provider: "Twelve Data",
			body: `{"code":401,"message":"Invalid API key"}`, instrument: false,
		},
		{
			name: "twelve quota", provider: "Twelve Data",
			body: `{"code":429,"message":"Limit exceeded"}`, instrument: false,
		},
		{
			name: "finnhub missing", provider: "Finnhub",
			body: `{"c":0}`, instrument: true,
		},
		{
			name: "finnhub api error", provider: "Finnhub",
			body: `{"error":"Invalid API key"}`, instrument: false,
		},
		{
			name: "pusula missing", provider: "Pusula Portföy",
			body: `{"items":[]}`, instrument: true,
		},
		{
			name: "izko missing", provider: "İZKO Gold",
			body: `{"success":true,"data":[]}`, instrument: true,
		},
		{
			name: "izko unavailable", provider: "İZKO Gold",
			body: `{"success":false}`, instrument: false,
		},
		{
			name: "bist missing", provider: "TradingView BIST",
			body: `{"data":[]}`, instrument: true,
		},
		{
			name: "crypto missing", provider: "TradingView Crypto",
			body: `{"data":[]}`, instrument: true,
		},
		{
			name: "coingecko missing", provider: "CoinGecko",
			body: `{}`, instrument: true,
		},
		{
			name: "fx missing", provider: "Frankfurter",
			body: `{"rates":{}}`, instrument: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			getter, ok := DefaultRegistry().Get(test.provider)
			if !ok {
				t.Fatal("missing getter")
			}

			providerTestClient(getter).Transport = failureBodyTransport{body: test.body}

			_, err := getter.GetPrice(t.Context(), "INVALID")
			if err == nil || isInstrumentFailure(err) != test.instrument {
				t.Fatalf(
					"error = %v, instrument-local = %v, want %v",
					err,
					isInstrumentFailure(err),
					test.instrument,
				)
			}
		})
	}
}

func TestResetFailuresClearsAllProviderState(t *testing.T) {
	t.Parallel()

	service := NewService(nil, NewRegistry())
	service.failures["test"] = 3
	service.breakers["test"] = true
	service.lastFail["test"] = time.Now()
	service.ResetFailures()

	if len(service.failures) != 0 || len(service.breakers) != 0 || len(service.lastFail) != 0 {
		t.Fatal("reset retained provider failure state")
	}
}

type endlessProviderBody struct{ bytesRead int }

func (body *endlessProviderBody) Read(data []byte) (int, error) {
	clear(data)
	body.bytesRead += len(data)

	return len(data), nil
}

func TestProviderBodyStopsReadingAtLimit(t *testing.T) {
	t.Parallel()

	body := &endlessProviderBody{}

	_, err := readProviderBody(body)
	if !errors.Is(err, errProviderResponseTooLarge) ||
		body.bytesRead != maxProviderResponseBytes+1 {
		t.Fatalf("read %d bytes, error %v", body.bytesRead, err)
	}
}

func assertInstrumentCacheFailures(t *testing.T, service *Service, provider string) {
	t.Helper()

	bad, err := service.getCachedPrice(provider, "BAD")
	if err != nil {
		t.Fatal(err)
	}

	good, err := service.getCachedPrice(provider, "GOOD")
	if err != nil {
		t.Fatal(err)
	}

	if bad.ErrorCount != breakerFailureThreshold || good.ErrorCount != 0 ||
		good.LastError != "" {
		t.Fatalf("failure bookkeeping leaked: bad=%+v good=%+v", bad, good)
	}
}

func seedAndFailInstrument(t *testing.T, service *Service, provider string) {
	t.Helper()

	quote := models.Price{
		Value:     10,
		Currency:  "USD",
		Source:    provider,
		FetchedAt: time.Now().Add(-time.Hour),
	}
	for _, instrument := range []string{"BAD", "GOOD"} {
		cacheErr := service.cachePrice(
			provider,
			instrument,
			&quote,
			-time.Minute,
		)
		if cacheErr != nil {
			t.Fatal(cacheErr)
		}
	}

	for range breakerFailureThreshold {
		stale, fetchErr := service.getPrice(t.Context(), provider, "BAD", true)
		if fetchErr != nil || !stale.IsFailed || !stale.IsStale {
			t.Fatalf("failure fallback = %+v, %v", stale, fetchErr)
		}
	}
}
