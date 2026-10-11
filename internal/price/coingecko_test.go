package price

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestCoinGeckoUsesHeaderAuthentication(t *testing.T) {
	const key = "test-demo-key"
	t.Setenv("COINGECKO_KEY", key)

	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			if request.URL.Query().Has("x_cg_demo_api_key") {
				t.Error("API key was included in the request URL")
			}

			if request.Header.Get("x-cg-demo-api-key") != key {
				t.Error("API key was not sent in the authentication header")
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"bitcoin":{"usd":100}}`))
		}),
	)
	t.Cleanup(server.Close)

	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	getter := NewCoinGecko()
	getter.client.Transport = &localProviderTransport{target: target}

	quote, err := getter.GetPrice(t.Context(), "bitcoin")
	if err != nil {
		t.Fatal(err)
	}

	if quote.Value != 100 || quote.Currency != "USD" {
		t.Fatalf("unexpected quote: %+v", quote)
	}
}
