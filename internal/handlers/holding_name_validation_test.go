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

func TestHoldingWritesRequireName(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodPost, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			for _, test := range []struct {
				label     string
				nameField string
				valid     bool
			}{
				{label: "missing", nameField: "", valid: false},
				{label: "empty", nameField: `"name":"",`, valid: false},
				{label: "valid", nameField: `"name":"My Holding",`, valid: true},
			} {
				t.Run(test.label, func(t *testing.T) {
					t.Parallel()
					checkHoldingNameWrite(t, method, test.nameField, test.valid)
				})
			}
		})
	}
}

func checkHoldingNameWrite(t *testing.T, method, nameField string, valid bool) {
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

	body := `{` + nameField + `"asset_type":"Stock","quantity":1,"base_currency":"TRY"}`
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
		"SELECT name FROM holdings ORDER BY id DESC LIMIT 1",
	)
	if err != nil {
		t.Fatal(err)
	}

	wantName := "Original"
	if valid {
		wantName = "My Holding"
	}

	if stored != wantName {
		t.Errorf("stored name = %q, want %q", stored, wantName)
	}
}
