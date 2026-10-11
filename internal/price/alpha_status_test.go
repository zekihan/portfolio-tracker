package price

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestAlphaVantageRejectsHTTPFailures(t *testing.T) {
	t.Setenv("ALPHA_VANTAGE_KEY", "test-key")

	for _, status := range []int{http.StatusOK, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"Global Quote":{"05. price":"100"}}`))
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
			if status != http.StatusOK {
				if err == nil {
					t.Errorf("accepted quote from HTTP %d", status)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if quote.Value != 100 || quote.Currency != "USD" {
				t.Fatalf("unexpected quote: %+v", quote)
			}
		})
	}
}
