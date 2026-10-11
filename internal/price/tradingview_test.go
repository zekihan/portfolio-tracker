package price

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestTradingViewValidatesQuotes(t *testing.T) {
	t.Parallel()

	for _, provider := range []string{"BIST", "Crypto"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()

			for _, test := range []struct {
				name    string
				body    string
				status  int
				wantErr bool
			}{
				{name: "server error", body: `[{"symbol":"TEST","price":100}]`, status: 500, wantErr: true},
				{name: "missing", body: `[{"symbol":"TEST"}]`, status: 200, wantErr: true},
				{name: "negative", body: `[{"symbol":"TEST","price":-100}]`, status: 200, wantErr: true},
				{name: "zero", body: `[{"symbol":"TEST","price":0}]`, status: 200, wantErr: true},
				{name: "valid", body: `[{"symbol":"TEST","price":100}]`, status: 200, wantErr: false},
			} {
				t.Run(test.name, func(t *testing.T) {
					t.Parallel()

					server := httptest.NewServer(
						http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
							w.Header().Set("Content-Type", "application/json")
							w.WriteHeader(test.status)
							_, _ = w.Write([]byte(test.body))
						}),
					)
					t.Cleanup(server.Close)

					target, err := url.Parse(server.URL)
					if err != nil {
						t.Fatal(err)
					}

					transport := &localProviderTransport{target: target}

					var getter Getter

					currency := "TRY"

					if provider == "BIST" {
						bist := NewTradingViewBIST()
						bist.client.Transport = transport
						getter = bist
					} else {
						crypto := NewTradingViewCrypto()
						crypto.client.Transport = transport
						getter = crypto
						currency = "USD"
					}

					quote, err := getter.GetPrice(t.Context(), "test")
					if (err != nil) != test.wantErr {
						t.Errorf("error = %v, want error: %v", err, test.wantErr)
					}

					if !test.wantErr && (quote.Value != 100 || quote.Currency != currency) {
						t.Errorf("unexpected quote: %+v", quote)
					}
				})
			}
		})
	}
}
