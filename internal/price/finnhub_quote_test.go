package price

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestFinnhubValidatesPrices(t *testing.T) {
	t.Setenv("FINNHUB_KEY", "test-key")

	for _, test := range []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "negative", body: `{"c":-100}`, wantErr: true},
		{name: "zero", body: `{"c":0}`, wantErr: true},
		{name: "missing", body: `{}`, wantErr: true},
		{name: "null", body: `{"c":null}`, wantErr: true},
		{name: "valid", body: `{"c":100}`, wantErr: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(test.body))
				}),
			)
			t.Cleanup(server.Close)

			target, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}

			getter := NewFinnhub()
			getter.client.Transport = &localProviderTransport{target: target}

			quote, err := getter.GetPrice(t.Context(), "TEST")
			if (err != nil) != test.wantErr {
				t.Errorf("error = %v, want error: %v", err, test.wantErr)
			}

			if !test.wantErr && (quote.Value != 100 || quote.Currency != "USD") {
				t.Errorf("unexpected quote: %+v", quote)
			}
		})
	}
}
