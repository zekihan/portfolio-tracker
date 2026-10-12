package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

func TestHoldingWritesValidateBaseCurrency(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodPost, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			for _, currency := range []string{"EUR", "", "TRY", "USD"} {
				t.Run("currency="+currency, func(t *testing.T) {
					t.Parallel()
					checkHoldingCurrencyWrite(t, method, currency)
				})
			}
		})
	}
}

func checkHoldingCurrencyWrite(t *testing.T, method, currency string) {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
  (id, name, asset_type, base_currency) VALUES (1, 'Original', 'Stock', 'TRY')`)
	if err != nil {
		t.Fatal(err)
	}

	handler := New(services.NewHoldingService(database, nil), nil, nil, nil)
	router := http.NewServeMux()
	router.HandleFunc("POST /holdings", handler.createHolding)
	router.HandleFunc("PUT /holdings/{id}", handler.updateHolding)

	path := "/holdings"
	wantStatus := http.StatusCreated

	if method == http.MethodPut {
		path += "/1"
		wantStatus = http.StatusOK
	}

	valid := currency == "TRY" || currency == "USD"
	if !valid {
		wantStatus = http.StatusBadRequest
	}

	body := fmt.Sprintf(
		`{"name":"Updated","asset_type":"Stock","quantity":1,"base_currency":%q}`,
		currency,
	)
	request := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")

	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != wantStatus {
		t.Errorf("status = %d, want %d", response.Code, wantStatus)
	}

	var count int

	err = database.GetContext(t.Context(), &count, "SELECT COUNT(*) FROM holdings")
	if err != nil {
		t.Fatal(err)
	}

	wantCount := 1
	if method == http.MethodPost && valid {
		wantCount = 2
	}

	if count != wantCount {
		t.Errorf("count = %d, want %d", count, wantCount)
	}

	var stored string

	err = database.GetContext(
		t.Context(),
		&stored,
		"SELECT base_currency FROM holdings ORDER BY id DESC LIMIT 1",
	)
	if err != nil {
		t.Fatal(err)
	}

	wantCurrency := currency
	if !valid {
		wantCurrency = "TRY"
	}

	if stored != wantCurrency {
		t.Errorf("stored currency = %q, want %q", stored, wantCurrency)
	}
}
