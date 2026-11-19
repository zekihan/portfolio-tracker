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

func TestFrankfurterRejectsInvalidRates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "null", body: `{"base":"USD","rates":{"TRY":null}}`, wantErr: true},
		{name: "zero", body: `{"base":"USD","rates":{"TRY":0}}`, wantErr: true},
		{name: "negative", body: `{"base":"USD","rates":{"TRY":-40}}`, wantErr: true},
		{name: "missing", body: `{"base":"USD","rates":{}}`, wantErr: true},
		{name: "valid", body: `{"base":"USD","rates":{"TRY":40}}`, wantErr: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(test.body))
				}),
			)
			t.Cleanup(server.Close)

			target, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}

			getter := NewFrankfurter()
			getter.client.Transport = &localProviderTransport{target: target}

			quote, err := getter.GetPrice(t.Context(), "USD")
			if (err != nil) != test.wantErr {
				t.Errorf("error = %v, want error: %v", err, test.wantErr)
			}

			if !test.wantErr && (quote.Value != 40 || quote.Currency != "TRY") {
				t.Errorf("unexpected rate: %+v", quote)
			}
		})
	}
}

func TestFrankfurterRefreshPreservesRateWhenBaseIsUnverified(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ name, body string }{
		{name: "wrong base", body: `{"base":"EUR","rates":{"TRY":50}}`},
		{name: "missing base", body: `{"rates":{"TRY":50}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			getter := NewFrankfurter()
			getter.client.Transport = failureBodyTransport{body: test.body}
			service := newRefreshTestService(t, getter)
			seedRecoveryFX(t, service, getter.Name(), "USD", 40, 10*time.Second)

			readCtx := WithCachedQuotesOnly(t.Context())
			before, err := service.GetUSDTTRYQuote(readCtx)
			checkFreshFXQuote(t, &before, err, 40)

			err = service.RefreshAll(t.Context(), []models.Holding{{BaseCurrency: "USD"}})
			if !errors.Is(err, errMalformedProviderResponse) {
				t.Errorf("refresh error = %v, want malformed response", err)
			}

			cached, err := service.GetUSDTTRYQuote(readCtx)
			if err != nil || cached.Value != before.Value || cached.Currency != before.Currency ||
				!cached.FetchedAt.Equal(before.FetchedAt) || !cached.IsStale || !cached.IsFailed {
				t.Errorf(
					"cached USD/TRY = %+v, %v; want prior rate marked stale and failed",
					cached,
					err,
				)
			}
		})
	}
}
