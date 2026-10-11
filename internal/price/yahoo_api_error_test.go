package price

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestYahooRejectsAPIErrors(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		apiError string
		wantErr  bool
	}{
		{name: "error with quote", apiError: `{"code":"Unauthorized","description":"Denied"}`, wantErr: true},
		{name: "null error", apiError: `null`, wantErr: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"chart":{"result":[{"meta":{
					"regularMarketPrice":100,"currency":"USD"}}],"error":` + test.apiError + `}}`))
				}),
			)
			t.Cleanup(server.Close)

			target, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}

			getter := NewYahooFinance()
			getter.client.Transport = &localProviderTransport{target: target}

			quote, err := getter.GetPrice(t.Context(), "TEST")
			if test.wantErr {
				if err == nil {
					t.Error("accepted quote despite API error")
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if quote.Value != 100 || quote.Currency != "USD" {
				t.Errorf("unexpected quote: %+v", quote)
			}
		})
	}
}
