package handlers

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

func TestPriceMutationsRequireExplicitValue(t *testing.T) {
	t.Parallel()

	for _, field := range []string{"price", "estPrice"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()

			for _, test := range []struct {
				name   string
				body   string
				status int
				want   float64
			}{
				{name: "empty object", body: `{}`, status: http.StatusBadRequest, want: 20},
				{name: "null object", body: `null`, status: http.StatusBadRequest, want: 20},
				{name: "null field", body: `{"` + field + `":null}`, status: http.StatusBadRequest, want: 20},
				{name: "misspelled field", body: `{"prcie":42}`, status: http.StatusBadRequest, want: 20},
				{name: "negative", body: `{"` + field + `":-1}`, status: http.StatusBadRequest, want: 20},
				{name: "wrong type", body: `{"` + field + `":"42"}`, status: http.StatusBadRequest, want: 20},
				{name: "zero", body: `{"` + field + `":0}`, status: http.StatusOK, want: 0},
				{name: "positive", body: `{"` + field + `":42}`, status: http.StatusOK, want: 42},
			} {
				t.Run(test.name, func(t *testing.T) {
					t.Parallel()
					checkPriceMutation(t, field, test.body, test.status, test.want)
				})
			}
		})
	}
}

func checkPriceMutation(t *testing.T, field, body string, status int, want float64) {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
       (id, name, asset_type, quantity, base_currency, manual_price, est_price)
       VALUES (1, 'Test', 'Cash', 1, 'TRY', 20, 20)`)
	if err != nil {
		t.Fatal(err)
	}

	handler := New(services.NewHoldingService(database, nil), nil, nil, nil)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPatch,
		"/api/holdings/1", strings.NewReader(body))
	request.SetPathValue("id", "1")

	response := httptest.NewRecorder()
	if field == "price" {
		handler.updateManualPrice(response, request)
	} else {
		handler.updateEstPrice(response, request)
	}

	if response.Code != status {
		t.Fatalf("status=%d want=%d body=%s", response.Code, status, response.Body)
	}

	var actual float64

	query := "SELECT manual_price FROM holdings WHERE id=1"
	if field == "estPrice" {
		query = "SELECT est_price FROM holdings WHERE id=1"
	}

	err = database.GetContext(t.Context(), &actual, query)
	if err != nil {
		t.Fatal(err)
	}

	if actual != want {
		t.Fatalf("saved=%v want=%v", actual, want)
	}
}
