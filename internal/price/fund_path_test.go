package price

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func fundPathHandler(t *testing.T, instrument, body string) http.Handler {
	t.Helper()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/fund/"+instrument {
			t.Errorf("decoded path = %q, want instrument %q", r.URL.Path, instrument)
		}

		if r.URL.RawQuery != "" || r.URL.Fragment != "" {
			t.Error("instrument escaped into query or fragment")
		}

		if r.URL.EscapedPath() != "/api/fund/"+url.PathEscape(instrument) {
			t.Errorf("unexpected escaped path: %q", r.URL.EscapedPath())
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
}

func TestFundProvidersPreserveInstrumentPaths(t *testing.T) {
	t.Parallel()

	for _, provider := range []struct {
		create func() (Getter, *http.Client)
		name   string
		body   string
	}{
		{
			name: "Pusula", body: `{"success":true,"data":{"price":100}}`,
			create: func() (Getter, *http.Client) {
				g := NewPusulaPortfoy()
				return g, g.client
			},
		},
		{
			name: "Garanti", body: `{"price":100}`,
			create: func() (Getter, *http.Client) {
				g := NewGarantiBBVA()
				return g, g.client
			},
		},
	} {
		t.Run(provider.name, func(t *testing.T) {
			t.Parallel()

			for _, instrument := range []string{"TEST", "FUND A", "A+B", "A/B?C#D"} {
				t.Run(instrument, func(t *testing.T) {
					t.Parallel()
					server := httptest.NewServer(fundPathHandler(t, instrument, provider.body))
					t.Cleanup(server.Close)

					target, err := url.Parse(server.URL)
					if err != nil {
						t.Fatal(err)
					}

					getter, client := provider.create()
					client.Transport = &localProviderTransport{target: target}

					quote, err := getter.GetPrice(t.Context(), instrument)
					if err != nil {
						t.Fatal(err)
					}

					if quote.Value != 100 || quote.Currency != "TRY" {
						t.Errorf("unexpected quote: %+v", quote)
					}
				})
			}
		})
	}
}
