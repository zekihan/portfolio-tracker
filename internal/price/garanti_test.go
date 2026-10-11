package price

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestGarantiValidatesQuotes(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		body    string
		status  int
		wantErr bool
	}{
		{name: "rate limited", status: http.StatusTooManyRequests, body: `{"data":{"Close":100}}`, wantErr: true},
		{name: "server error", status: http.StatusInternalServerError, body: `{"data":{"Close":100}}`, wantErr: true},
		{name: "negative", status: http.StatusOK, body: `{"data":{"Close":-100}}`, wantErr: true},
		{name: "missing", status: http.StatusOK, body: `{}`, wantErr: true},
		{name: "valid", status: http.StatusOK, body: `{"data":{"Close":100}}`, wantErr: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/webservice/lastdate" {
						_, _ = w.Write([]byte(`"2026-10-09"`))

						return
					}

					if r.URL.Path == "/fon-getirileri" {
						_, _ = w.Write(
							[]byte(`<option data-code="TEST" data-currency="TRY"></option>`),
						)

						return
					}

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

			getter := NewGarantiBBVA()
			getter.client.Transport = &localProviderTransport{target: target}

			quote, err := getter.GetPrice(t.Context(), "TEST")
			if (err != nil) != test.wantErr {
				t.Errorf("error = %v, want error: %v", err, test.wantErr)
			}

			if !test.wantErr && (quote.Value != 100 || quote.Currency != "TRY") {
				t.Errorf("unexpected quote: %+v", quote)
			}
		})
	}
}
