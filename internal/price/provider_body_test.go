package price

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestProvidersRejectTruncatedResponses(t *testing.T) {
	t.Setenv("ALPHA_VANTAGE_KEY", "test-key")
	t.Setenv("TWELVE_DATA_KEY", "test-key")
	t.Setenv("FINNHUB_KEY", "test-key")

	for _, test := range []struct {
		name string
		body string
	}{
		{name: "Alpha Vantage", body: `{"Global Quote":{"05. price":"100"}}`},
		{name: "Twelve Data", body: `{"price":"100"}`},
		{name: "Finnhub", body: `{"c":100}`},
		{name: "Pusula Portföy", body: `{"success":true,"data":{"price":100}}`},
		{name: "Garanti BBVA", body: `{"price":100}`},
		{name: "İZKO Gold", body: `[{"name":"TEST","price":100}]`},
		{name: "TradingView BIST", body: `[{"symbol":"TEST","price":100}]`},
		{name: "TradingView Crypto", body: `[{"symbol":"TEST","price":100}]`},
		{name: "CoinGecko", body: `{"TEST":{"usd":100}}`},
		{name: "Frankfurter", body: `{"rates":{"TRY":40}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Length", "1000")
					_, _ = w.Write([]byte(test.body))
				}),
			)
			t.Cleanup(server.Close)

			target, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}

			getter, ok := DefaultRegistry().Get(test.name)
			if !ok {
				t.Fatalf("missing provider %s", test.name)
			}

			client := providerTestClient(getter)
			client.Transport = &localProviderTransport{target: target}

			quote, err := getter.GetPrice(t.Context(), "TEST")
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("error = %v, want unexpected EOF; quote = %+v", err, quote)
			}
		})
	}
}

func providerTestClient(getter Getter) *http.Client {
	switch provider := getter.(type) {
	case *YahooFinance:
		return provider.client
	case *AlphaVantage:
		return provider.client
	case *TwelveData:
		return provider.client
	case *Finnhub:
		return provider.client
	case *PusulaPortfoy:
		return provider.client
	case *GarantiBBVA:
		return provider.client
	case *IZKOGold:
		return provider.client
	case *TradingViewBIST:
		return provider.client
	case *TradingViewCrypto:
		return provider.client
	case *CoinGecko:
		return provider.client
	case *Frankfurter:
		return provider.client
	default:
		panic("unsupported test provider")
	}
}
