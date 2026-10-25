package price

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestEveryProviderRejectsRedirects(t *testing.T) {
	t.Setenv("ALPHA_VANTAGE_KEY", "redirect-test-alpha-secret")
	t.Setenv("TWELVE_DATA_KEY", "redirect-test-twelve-secret")
	t.Setenv("FINNHUB_KEY", "redirect-test-finnhub-secret")
	t.Setenv("COINGECKO_KEY", "redirect-test-coingecko-secret")

	for _, location := range []string{
		"http://127.0.0.1:1/private",
		"https://10.0.0.1/private",
		"https://172.16.0.1/private",
		"https://192.168.0.1/private",
		"http://169.254.169.254/latest/meta-data",
		"http://[::1]:1/private",
		"http://[fe80::1]/private",
		"https://attacker.invalid/steal",
		"https://rebind.invalid/private",
		"/same-origin?apikey=redirect-test-location-secret",
	} {
		t.Run(location, func(t *testing.T) {
			for _, getter := range DefaultRegistry().All() {
				if getter.Name() == manualProviderName {
					continue
				}

				t.Run(getter.Name(), func(t *testing.T) {
					var requests atomic.Int32

					server := httptest.NewServer(
						http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
							requests.Add(1)
							w.Header().Set("Location", location)
							w.WriteHeader(http.StatusFound)
						}),
					)
					defer server.Close()

					setRegressionTransport(t, providerTestClient(getter), server.URL)

					_, err := getter.GetPrice(t.Context(), "TEST")
					if !errors.Is(err, errProviderRedirectDenied) {
						t.Fatalf("redirect error = %v", err)
					}

					if requests.Load() != 1 {
						t.Fatalf("redirect was followed: %d requests", requests.Load())
					}

					if strings.Contains(err.Error(), "redirect-test-") {
						t.Fatalf("redirect error exposed credentials: %v", err)
					}
				})
			}
		})
	}
}

func TestAlphaCurrencyRejectsRedirects(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Location", "https://attacker.invalid/currency")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	getter := NewAlphaVantage()
	setRegressionTransport(t, getter.client, server.URL)

	_, err := getter.fetchQuoteCurrency(t.Context(), "TEST", "metadata-secret")
	if !errors.Is(err, errProviderRedirectDenied) || requests.Load() != 1 {
		t.Fatalf("metadata redirect: requests=%d error=%v", requests.Load(), err)
	}

	if strings.Contains(err.Error(), "metadata-secret") {
		t.Fatalf("metadata error exposed credentials: %v", err)
	}
}

func TestRedirectsNeverReachCredentialSink(t *testing.T) {
	t.Parallel()

	for _, status := range []int{
		http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			var received atomic.Int32

			sink := httptest.NewServer(
				http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
					received.Add(1)
				}),
			)
			defer sink.Close()

			source := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					// localhost and 127.0.0.1 are distinct origins for HTTP credential forwarding.
					w.Header().
						Set("Location", strings.Replace(sink.URL, "127.0.0.1", "localhost", 1))
					w.WriteHeader(status)
				}),
			)
			defer source.Close()

			response, err := doGet(
				t.Context(),
				newProviderClient(),
				source.URL+"?apikey=query-secret",
				map[string]string{
					"X-Finnhub-Token": "finnhub-secret", "X-Cg-Demo-Api-Key": "coingecko-secret",
				},
			)
			if response != nil {
				_ = response.Body.Close()

				t.Fatal("redirect returned a usable response")
			}

			if !errors.Is(err, errProviderRedirectDenied) || received.Load() != 0 {
				t.Fatalf("credential sink received %d requests; error=%v", received.Load(), err)
			}

			if strings.Contains(err.Error(), "query-secret") {
				t.Fatalf("redirect error exposed query secret: %v", err)
			}
		})
	}
}
