package handlers

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

func TestHoldingWritesValidateEstimatedPrice(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodPost, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			for _, test := range []struct {
				name  string
				price string
				value float64
				valid bool
			}{
				{name: "negative", price: "-1", value: 20, valid: false},
				{name: "zero", price: "0", value: 0, valid: true},
				{name: "positive", price: "12.5", value: 12.5, valid: true},
			} {
				t.Run(test.name, func(t *testing.T) {
					t.Parallel()
					checkHoldingEstimateWrite(t, method, test.price, test.value, test.valid)
				})
			}
		})
	}
}

func checkHoldingEstimateWrite(t *testing.T, method, price string, value float64, valid bool) {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
  (id, name, asset_type, est_price) VALUES (1, 'Original', 'Stock', 20)`)
	if err != nil {
		t.Fatal(err)
	}

	handler := New(services.NewHoldingService(database, nil), nil, nil, nil)
	router := gin.New()
	router.POST("/holdings", handler.createHolding)
	router.PUT("/holdings/:id", handler.updateHolding)

	path := "/holdings"
	wantStatus := http.StatusCreated

	if method == http.MethodPut {
		path += "/1"
		wantStatus = http.StatusOK
	}

	if !valid {
		wantStatus = http.StatusBadRequest
	}

	body := `{"name":"Updated","asset_type":"Stock","base_currency":"TRY","quantity":1,"est_price":` + price + `}`
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
		t.Errorf("holding count = %d, want %d", count, wantCount)
	}

	var stored float64

	err = database.GetContext(
		t.Context(),
		&stored,
		"SELECT est_price FROM holdings ORDER BY id DESC LIMIT 1",
	)
	if err != nil {
		t.Fatal(err)
	}

	if stored != value {
		t.Errorf("stored estimate = %v, want %v", stored, value)
	}
}
