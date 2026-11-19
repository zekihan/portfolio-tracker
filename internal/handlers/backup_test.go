package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

func TestDownloadBackupIgnoresRange(t *testing.T) {
	t.Chdir("../..")

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	settings := services.NewSettingsService(database)

	err = settings.SetDisplayCurrency(t.Context(), "USD")
	if err != nil {
		t.Fatal(err)
	}

	router := New(nil, nil, settings, nil).SetupRoutes()

	request := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/api/settings/backup",
		http.NoBody,
	)
	request.Header.Set("X-Portfolio-Action", "1")
	request.Header.Set("Range", "bytes=0-15")

	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}

	if contentRange := response.Header().Get("Content-Range"); contentRange != "" {
		t.Errorf("unexpected Content-Range %q", contentRange)
	}

	disposition := response.Header().Get("Content-Disposition")
	if !strings.HasPrefix(disposition, "attachment; filename=\"portfolio-backup-") {
		t.Errorf("Content-Disposition = %q, want attachment", disposition)
	}

	if !strings.HasPrefix(response.Body.String(), "SQLite format 3\x00") {
		t.Fatal("response body is not a SQLite database")
	}

	path := filepath.Join(t.TempDir(), "downloaded.db")

	err = os.WriteFile(path, response.Body.Bytes(), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	downloaded, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = downloaded.Close() })

	saved, err := services.NewSettingsService(downloaded).GetAll(t.Context())
	if err != nil || saved.DisplayCurrency != "USD" {
		t.Fatalf("downloaded database settings = %+v, %v; want USD", saved, err)
	}
}
