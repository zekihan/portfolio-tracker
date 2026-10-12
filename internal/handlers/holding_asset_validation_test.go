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

func TestHoldingWritesValidateAssetType(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodPost, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			for _, assetType := range []string{"Unsupported", "", "Stock", "ETF", "TR Fund", "Crypto", "Gold", "Cash"} {
				t.Run("assetType="+assetType, func(t *testing.T) {
					t.Parallel()
					checkHoldingAssetTypeWrite(t, method, assetType)
				})
			}
		})
	}
}

func checkHoldingAssetTypeWrite(t *testing.T, method, assetType string) {
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

	valid := assetType != "Unsupported" && assetType != ""
	if !valid {
		wantStatus = http.StatusBadRequest
	}

	body := fmt.Sprintf(
		`{"name":"Updated","asset_type":%q,"quantity":1,"base_currency":"TRY"}`,
		assetType,
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
		"SELECT asset_type FROM holdings ORDER BY id DESC LIMIT 1",
	)
	if err != nil {
		t.Fatal(err)
	}

	wantAssetType := assetType
	if !valid {
		wantAssetType = "Stock"
	}

	if stored != wantAssetType {
		t.Errorf("stored assetType = %q, want %q", stored, wantAssetType)
	}
}
