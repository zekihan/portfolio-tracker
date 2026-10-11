package price

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestAlphaVantageValidatesPrices(t *testing.T) {
	t.Setenv("ALPHA_VANTAGE_KEY", "test-key")

	for _, test := range []struct {
		input   string
		wantErr bool
	}{
		{input: "12oops", wantErr: true},
		{input: "NaN", wantErr: true},
		{input: "+Inf", wantErr: true},
		{input: "-1", wantErr: true},
		{input: "0", wantErr: true},
		{input: "", wantErr: true},
		{input: "12.50", wantErr: false},
	} {
		t.Run(test.input, func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"Global Quote":{"05. price":"` + test.input + `"}}`))
				}),
			)
			t.Cleanup(server.Close)

			target, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}

			getter := NewAlphaVantage()
			getter.client.Transport = &localProviderTransport{target: target}

			quote, err := getter.GetPrice(t.Context(), "TEST")
			if (err != nil) != test.wantErr {
				t.Errorf("error = %v, want error: %v", err, test.wantErr)
			}

			if !test.wantErr && (quote.Value != 12.5 || quote.Currency != "USD") {
				t.Errorf("unexpected quote: %+v", quote)
			}
		})
	}
}
