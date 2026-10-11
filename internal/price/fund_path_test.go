package price

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestGarantiRequestAndQuoteCurrency(t *testing.T) {
	t.Parallel()

	for _, code := range []string{"GTL", "GRO", "A/B?C#D"} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()

			currency := "TRY"
			if code == "GRO" {
				currency = "USD"
			}

			server := httptest.NewServer(
				garantiTestHandler(t, code, currency),
			)
			defer server.Close()

			target, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}

			getter := NewGarantiBBVA()
			getter.client.Transport = &localProviderTransport{target: target}

			quote, err := getter.GetPrice(t.Context(), code)
			if err != nil {
				t.Fatal(err)
			}

			if quote.Currency != currency || quote.Value != 1.1392477517637816 {
				t.Errorf("unexpected quote: %+v", quote)
			}
		})
	}
}

func TestGoldDerivedPrices(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/web/v1/gold-prices" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}

		_, _ = w.Write(
			[]byte(
				`{"success":true,"data":[{"key":"ata","sell_price":44450},
 {"key":"hasaltin","sell_price":6643},
 {"key":"yirmiiki","sell_price":6250}]}`,
			),
		)
	}))
	defer server.Close()

	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	getter := NewIZKOGold()

	getter.client.Transport = &localProviderTransport{target: target}
	for _, test := range []struct {
		key    string
		source string
		value  float64
	}{
		{key: "resat", value: 44450, source: "İZKO Gold (Ata proxy)"},
		{key: "gremse", value: 106288, source: "İZKO Gold (16g has altın estimate)"},
		{key: "yirmiiki", value: 6250, source: "İZKO Gold"},
	} {
		quote, err := getter.GetPrice(t.Context(), test.key)
		if err != nil {
			t.Fatal(err)
		}

		if quote.Value != test.value || quote.Source != test.source {
			t.Errorf("%s: %+v", test.key, quote)
		}
	}
}

func garantiTestHandler(t *testing.T, code, currency string) http.Handler {
	t.Helper()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/webservice/lastdate":
			if r.Method != http.MethodPost {
				t.Error("date must use POST")
			}

			_, _ = w.Write([]byte(`"2026-10-09"`))
		case "/webservice/funddailyvalues":
			var payload map[string]string

			err := json.NewDecoder(r.Body).Decode(&payload)
			if err != nil {
				t.Error(err)
			}

			if payload["code"] != code || payload["date"] != "2026-10-09" ||
				payload["rollBack"] != "T" {
				t.Errorf("unexpected request: %v", payload)
			}
			// Garanti wraps the JSON object in a JSON string.
			_ = json.NewEncoder(w).Encode(`{"data":{"Close":1.1392477517637816}}`)
		case "/fon-getirileri":
			_, _ = fmt.Fprintf(
				w,
				`<select><option data-currency="%s" data-code="%s"></option></select>`,
				currency,
				code,
			)
		default:
			t.Errorf("unexpected request path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}
