package price

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestYahooPreservesInstrumentPath(t *testing.T) {
	t.Parallel()

	for _, instrument := range []string{"TEST", "BRK B", "A+B", "A/B?C#D"} {
		t.Run(instrument, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if got, want := r.URL.EscapedPath(), "/v8/finance/chart/"+url.PathEscape(
						instrument,
					); got != want {
						t.Errorf("request path = %q, want %q", got, want)
					}

					if r.URL.Query().Get("interval") != "1m" || r.URL.Query().Get("range") != "1d" {
						t.Error("chart query parameters changed")
					}

					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write(
						[]byte(
							`{"chart":{"result":[{"meta":{"regularMarketPrice":100,"currency":"USD"}}]}}`,
						),
					)
				}),
			)
			t.Cleanup(server.Close)

			target, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}

			getter := NewYahooFinance()
			getter.client.Transport = &localProviderTransport{target: target}

			quote, err := getter.GetPrice(t.Context(), instrument)
			if err != nil {
				t.Fatal(err)
			}

			if quote.Value != 100 || quote.Currency != "USD" {
				t.Errorf("unexpected quote: %+v", quote)
			}
		})
	}
}
