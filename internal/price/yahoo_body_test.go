package price

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestYahooRejectsTruncatedResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write(
			[]byte(`{"chart":{"result":[{"meta":{"regularMarketPrice":100,"currency":"USD"}}]}}`),
		)
	}))
	t.Cleanup(server.Close)

	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	getter := NewYahooFinance()
	getter.client.Transport = &localProviderTransport{target: target}

	quote, err := getter.GetPrice(t.Context(), "TEST")
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("error = %v, want unexpected EOF; quote = %+v", err, quote)
	}
}
