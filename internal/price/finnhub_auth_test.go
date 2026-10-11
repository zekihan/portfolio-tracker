package price

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestFinnhubUsesHeaderAuthentication(t *testing.T) {
	const key = "test-finnhub-key"
	t.Setenv("FINNHUB_KEY", key)

	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			if request.URL.Query().Has("token") {
				t.Error("API key was included in the request URL")
			}

			if request.Header.Get("X-Finnhub-Token") != key {
				t.Error("API key was not sent in the authentication header")
			}

			if request.URL.Query().Get("symbol") != "BRK B" {
				t.Error("instrument was not preserved in request")
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"c":100}`))
		}),
	)
	t.Cleanup(server.Close)

	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	getter := NewFinnhub()
	getter.client.Transport = &localProviderTransport{target: target}

	quote, err := getter.GetPrice(t.Context(), "BRK B")
	if err != nil {
		t.Fatal(err)
	}

	if quote.Value != 100 || quote.Currency != "USD" {
		t.Fatalf("unexpected quote: %+v", quote)
	}
}
