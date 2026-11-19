package price

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

func TestYahooFinanceRejectsInvalidQuotePrices(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		meta    string
		wantErr bool
	}{
		{name: "missing", meta: `{"currency":"USD"}`, wantErr: true},
		{name: "null", meta: `{"regularMarketPrice":null,"currency":"USD"}`, wantErr: true},
		{name: "zero", meta: `{"regularMarketPrice":0,"currency":"USD"}`, wantErr: true},
		{name: "negative", meta: `{"regularMarketPrice":-10,"currency":"USD"}`, wantErr: true},
		{name: "valid", meta: `{"regularMarketPrice":100,"currency":"USD"}`, wantErr: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"chart":{"result":[{"meta":` + test.meta + `}]}}`))
				}),
			)
			t.Cleanup(server.Close)

			target, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}

			getter := NewYahooFinance()
			getter.client.Transport = &localProviderTransport{target: target}

			quote, err := getter.GetPrice(t.Context(), "TEST")
			if (err != nil) != test.wantErr {
				t.Errorf("error = %v, want error: %v", err, test.wantErr)
			}

			if !test.wantErr && quote.Value != 100 {
				t.Errorf("quote = %v, want 100", quote.Value)
			}
		})
	}
}

func TestYahooRefreshPreservesCachedQuoteWhenCurrencyIsMissing(t *testing.T) {
	t.Parallel()

	getter := NewYahooFinance()
	getter.client.Transport = failureBodyTransport{
		body: `{"chart":{"result":[{"meta":{"regularMarketPrice":125}}]}}`,
	}
	service := newRefreshTestService(t, getter)

	const instrument = "TEST.IS"

	old := models.Price{
		Value: 100, Currency: "TRY", Source: getter.Name(),
		FetchedAt: time.Now().UTC().Add(-time.Minute).Truncate(time.Second),
	}

	err := service.cachePrice(getter.Name(), instrument, &old, getter.TTL())
	if err != nil {
		t.Fatal(err)
	}

	err = service.RefreshAll(t.Context(), []models.Holding{
		{PriceProvider: getter.Name(), InstrumentID: instrument},
	})
	if !errors.Is(err, errUnknownQuoteCurrency) {
		t.Errorf("refresh error = %v, want %v", err, errUnknownQuoteCurrency)
	}

	cached, err := service.GetPrice(WithCachedQuotesOnly(t.Context()), getter.Name(), instrument)
	if err != nil || cached.Value != old.Value || cached.Currency != old.Currency ||
		!cached.FetchedAt.Equal(old.FetchedAt) || !cached.IsStale || !cached.IsFailed {
		t.Errorf(
			"cached quote = %+v, %v; want prior TRY quote marked stale and failed",
			cached,
			err,
		)
	}
}
