package price

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestAlphaVantageRejectsAPIErrorSignals(t *testing.T) {
	for _, field := range []string{"Error Message", "Information", "Note", ""} {
		name := field
		if name == "" {
			name = "valid"
		}

		t.Run(name, func(t *testing.T) {
			t.Setenv("ALPHA_VANTAGE_KEY", "synthetic-key")

			body := map[string]any{
				"Global Quote": map[string]string{"05. price": "100", "08. currency": "USD"},
			}
			if field != "" {
				body[field] = "Request denied"
			}

			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")

					err := json.NewEncoder(w).Encode(body)
					if err != nil {
						t.Error(err)
					}
				}),
			)
			t.Cleanup(server.Close)

			target, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}

			getter := NewAlphaVantage()
			getter.client.Transport = &localProviderTransport{target: target}

			quote, err := getter.GetPrice(t.Context(), "TEST")
			if field != "" {
				if err == nil {
					t.Errorf("accepted quote with %q API error", field)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if quote.Value != 100 || quote.Currency != "USD" {
				t.Errorf("unexpected quote: %+v", quote)
			}
		})
	}
}
