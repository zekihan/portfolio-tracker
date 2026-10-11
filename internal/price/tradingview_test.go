package price

import (
	"encoding/json"
	"fmt"
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
				{name: "server error", body: `{"data":[{"s":"%s","d":[100,"%s"]}]}`, status: 500, wantErr: true},
				{name: "missing", body: `{"data":[{"s":"%s","d":[null,"%s"]}]}`, status: 200, wantErr: true},
				{name: "negative", body: `{"data":[{"s":"%s","d":[-100,"%s"]}]}`, status: 200, wantErr: true},
				{name: "zero", body: `{"data":[{"s":"%s","d":[0,"%s"]}]}`, status: 200, wantErr: true},
				{name: "valid", body: `{"data":[{"s":"%s","d":[100,"%s"]}]}`, status: 200, wantErr: false},
			} {
				t.Run(test.name, func(t *testing.T) {
					t.Parallel()

					server := httptest.NewServer(
						tradingViewTestHandler(t, provider, test.body, test.status),
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

func tradingViewTestHandler(t *testing.T, provider, body string, status int) http.Handler {
	t.Helper()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Symbols struct {
				Tickers []string `json:"tickers"`
			} `json:"symbols"`
		}

		err := json.NewDecoder(r.Body).Decode(&request)
		if err != nil {
			t.Error(err)

			return
		}

		if len(request.Symbols.Tickers) != 1 {
			t.Error("missing ticker")

			return
		}

		currency := "TRY"
		if provider == "Crypto" {
			currency = "USD"
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, body, request.Symbols.Tickers[0], currency)
	})
}

func TestTradingViewNormalizesSheetSymbols(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ input, ticker, market, currency string }{
		{input: "ALTIN.S1", ticker: "BIST:ALTIN", market: "turkey", currency: "TRY"},
		{input: "ETH-USD", ticker: "CRYPTO:ETHUSD", market: "coin", currency: "USD"},
		{input: "CURRENCY:BNBUSD", ticker: "CRYPTO:BNBUSD", market: "coin", currency: "USD"},
		{input: "CURRENCY:XMRUSD", ticker: "CRYPTO:XMRUSD", market: "coin", currency: "USD"},
		{input: "TRX", ticker: "CRYPTO:TRXUSD", market: "coin", currency: "USD"},
		{input: "USDT", ticker: "CRYPTO:USDTUSD", market: "coin", currency: "USD"},
		{input: "USDC", ticker: "CRYPTO:USDCUSD", market: "coin", currency: "USD"},
	} {
		t.Run(test.input, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/"+test.market+"/scan" || r.Method != http.MethodPost {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					}

					var payload struct {
						Symbols struct {
							Tickers []string `json:"tickers"`
						} `json:"symbols"`
					}

					err := json.NewDecoder(r.Body).Decode(&payload)
					if err != nil {
						t.Error(err)
					}

					if len(payload.Symbols.Tickers) != 1 ||
						payload.Symbols.Tickers[0] != test.ticker {
						t.Errorf("unexpected tickers: %v", payload.Symbols.Tickers)
					}

					_, _ = fmt.Fprintf(
						w,
						`{"data":[{"s":%q,"d":[1,%q]}]}`,
						test.ticker,
						test.currency,
					)
				}),
			)
			defer server.Close()

			target, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}

			var getter Getter = NewTradingViewCrypto()
			if test.market == "turkey" {
				getter = NewTradingViewBIST()
			}

			providerTestClient(getter).Transport = &localProviderTransport{target: target}

			_, err = getter.GetPrice(t.Context(), test.input)
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
