package price

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
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
