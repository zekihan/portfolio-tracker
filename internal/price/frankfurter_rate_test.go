package price

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestFrankfurterRejectsInvalidRates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "null", body: `{"rates":{"TRY":null}}`, wantErr: true},
		{name: "zero", body: `{"rates":{"TRY":0}}`, wantErr: true},
		{name: "negative", body: `{"rates":{"TRY":-40}}`, wantErr: true},
		{name: "missing", body: `{"rates":{}}`, wantErr: true},
		{name: "valid", body: `{"rates":{"TRY":40}}`, wantErr: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

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

			getter := NewFrankfurter()
			getter.client.Transport = &localProviderTransport{target: target}

			quote, err := getter.GetPrice(t.Context(), "USD")
			if (err != nil) != test.wantErr {
				t.Errorf("error = %v, want error: %v", err, test.wantErr)
			}

			if !test.wantErr && (quote.Value != 40 || quote.Currency != "TRY") {
				t.Errorf("unexpected rate: %+v", quote)
			}
		})
	}
}
