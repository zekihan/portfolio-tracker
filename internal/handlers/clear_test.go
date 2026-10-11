package handlers

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

func TestClearAllDataEndpoint(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.ExecContext(
		t.Context(),
		`INSERT INTO holdings (name, asset_type) VALUES ('Stock', 'Stock')`,
	)
	if err != nil {
		t.Fatal(err)
	}

	handler := New(
		services.NewHoldingService(database, nil),
		nil,
		services.NewSettingsService(database),
		nil,
	)
	router := gin.New()
	router.DELETE("/api/settings/clear-all", handler.clearAllData)

	request := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodDelete,
		"/api/settings/clear-all",
		http.NoBody,
	)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}

	if got := response.Header().Get("HX-Refresh"); got != "true" {
		t.Errorf("HX-Refresh = %q, want true", got)
	}

	var count int

	countErr := database.GetContext(t.Context(), &count, "SELECT COUNT(*) FROM holdings")
	if countErr != nil {
		t.Fatal(countErr)
	}

	if count != 0 {
		t.Errorf("holdings = %d after clear, want 0", count)
	}
}
