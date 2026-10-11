package price

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

type localProviderTransport struct {
	target *url.URL
}

func (transport *localProviderTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	local := request.Clone(request.Context())
	local.URL.Scheme = transport.target.Scheme
	local.URL.Host = transport.target.Host

	response, err := http.DefaultTransport.RoundTrip(local)
	if err != nil {
		return nil, fmt.Errorf("local provider request: %w", err)
	}

	return response, nil
}

func TestYahooFinanceRejectsHTTPFailures(t *testing.T) {
	t.Parallel()

	for _, status := range []int{
		http.StatusOK, http.StatusTooManyRequests,
		http.StatusInternalServerError,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"chart":{"result":[{"meta":{
					"regularMarketPrice":100,"currency":"USD"}}],"error":null}}`))
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
			if status != http.StatusOK {
				if err == nil {
					t.Errorf("accepted a quote from HTTP %d", status)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if quote.Value != 100 || quote.Currency != "USD" {
				t.Fatalf("unexpected successful quote: %+v", quote)
			}
		})
	}
}
