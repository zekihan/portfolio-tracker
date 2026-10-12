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

func TestDownloadBackupReturnsSQLiteFile(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	handler := New(
		services.NewHoldingService(database, nil),
		nil,
		services.NewSettingsService(database),
		nil,
	)
	router := http.NewServeMux()
	router.HandleFunc("GET /api/settings/backup", handler.downloadBackup)

	request := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/api/settings/backup",
		http.NoBody,
	)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}

	disposition := response.Header().Get("Content-Disposition")
	if !strings.HasPrefix(disposition, "attachment; filename=\"portfolio-backup-") {
		t.Errorf("Content-Disposition = %q, want attachment", disposition)
	}

	if !strings.HasPrefix(response.Body.String(), "SQLite format 3\x00") {
		t.Error("response body is not a SQLite database")
	}
}
