package handlers

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

func restoreRequest(t *testing.T, fileName string, content []byte, confirm bool) *http.Request {
	t.Helper()

	var body bytes.Buffer

	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatal(err)
	}

	_, err = part.Write(content)
	if err != nil {
		t.Fatal(err)
	}

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/api/settings/restore",
		&body,
	)
	request.Header.Set("Content-Type", writer.FormDataContentType())

	if confirm {
		request.Header.Set("X-Restore-Confirm", "restore")
	}

	return request
}

func TestRestoreEndpoint(t *testing.T) {
	t.Parallel()

	source, err := db.Open(filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = source.Close() })

	_, err = source.ExecContext(
		t.Context(),
		`INSERT INTO holdings (name, asset_type) VALUES ('From backup', 'Stock')`,
	)
	if err != nil {
		t.Fatal(err)
	}

	backupPath := filepath.Join(t.TempDir(), "backup.db")

	err = services.NewSettingsService(source).Backup(t.Context(), backupPath)
	if err != nil {
		t.Fatal(err)
	}

	backupBytes, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}

	live, err := db.Open(filepath.Join(t.TempDir(), "live.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = live.Close() })

	handler := New(
		services.NewHoldingService(live, nil),
		nil,
		services.NewSettingsService(live),
		nil,
	)
	router := http.NewServeMux()
	router.Handle(
		"POST /api/settings/restore",
		requireRestoreConfirmation()(http.HandlerFunc(handler.restoreBackup)),
	)

	tests := []struct {
		name    string
		file    string
		content []byte
		confirm bool
		status  int
	}{
		{
			name:    "without confirmation",
			file:    "b.db",
			content: backupBytes,
			confirm: false,
			status:  http.StatusForbidden,
		},
		{
			name:    "not sqlite",
			file:    "b.db",
			content: []byte("plain text"),
			confirm: true,
			status:  http.StatusBadRequest,
		},
		{
			name:    "valid backup",
			file:    "b.db",
			content: backupBytes,
			confirm: true,
			status:  http.StatusOK,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			response := httptest.NewRecorder()
			router.ServeHTTP(response, restoreRequest(t, test.file, test.content, test.confirm))

			if response.Code != test.status {
				t.Errorf(
					"status = %d, want %d; body: %s",
					response.Code,
					test.status,
					response.Body.String(),
				)
			}
		})
	}
}
