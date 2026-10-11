package price

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestFrankfurterRejectsHTTPFailures(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusOK, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"rates":{"TRY":40},"date":"2026-10-11"}`))
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
			if status != http.StatusOK {
				if err == nil {
					t.Errorf("accepted rate from HTTP %d", status)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if quote.Value != 40 || quote.Currency != "TRY" {
				t.Fatalf("unexpected rate: %+v", quote)
			}
		})
	}
}
