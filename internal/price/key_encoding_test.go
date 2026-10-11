package price

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestProviderKeysPreserveQueryParameters(t *testing.T) {
	const key = "synthetic+key&symbol=WRONG#suffix"

	for _, provider := range []struct {
		create func() (Getter, *http.Client)
		name   string
		env    string
		body   string
	}{
		{
			name: "Alpha Vantage", env: "ALPHA_VANTAGE_KEY",
			body: `{"Global Quote":{"05. price":"100","08. currency":"USD"}}`,
			create: func() (Getter, *http.Client) {
				g := NewAlphaVantage()
				return g, g.client
			},
		},
		{
			name: "Twelve Data", env: "TWELVE_DATA_KEY", body: `{"price":"100","currency":"USD"}`,
			create: func() (Getter, *http.Client) {
				g := NewTwelveData()
				return g, g.client
			},
		},
	} {
		t.Run(provider.name, func(t *testing.T) {
			t.Setenv(provider.env, key)

			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					query := r.URL.Query()
					if query.Get("apikey") != key {
						t.Error("API key was not preserved")
					}

					if symbols := query["symbol"]; len(symbols) != 1 || symbols[0] != "BRK B" {
						t.Errorf("symbol parameters = %v, want [BRK B]", symbols)
					}

					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(provider.body))
				}),
			)
			t.Cleanup(server.Close)

			target, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}

			getter, client := provider.create()
			client.Transport = &localProviderTransport{target: target}

			quote, err := getter.GetPrice(t.Context(), "BRK B")
			if err != nil {
				t.Fatal(err)
			}

			if quote.Value != 100 || quote.Currency != "USD" {
				t.Errorf("unexpected quote: %+v", quote)
			}
		})
	}
}
