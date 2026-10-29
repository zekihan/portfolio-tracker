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

func TestStreamingJSONBodyLimit(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, body string
		want       int
	}{
		{
			name: "oversized value",
			body: `{"mode":"` + strings.Repeat("a", maxRequestBodyBytes) + `"}`,
			want: http.StatusRequestEntityTooLarge,
		},
		{
			name: "oversized trailing whitespace",
			body: `{"mode":"dark"}` + strings.Repeat(" ", maxRequestBodyBytes),
			want: http.StatusRequestEntityTooLarge,
		},
		{
			name: "exact limit",
			body: `{"mode":"dark"}` + strings.Repeat(" ", maxRequestBodyBytes-len(`{"mode":"dark"}`)),
			want: http.StatusOK,
		},
		{name: "invalid JSON", body: `{"mode":`, want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			settings := services.NewSettingsService(database)
			handler := New(nil, nil, settings, nil)
			request := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPut,
				"/settings/dark-mode",
				strings.NewReader(test.body),
			)
			request.ContentLength = -1 // Chunked requests have no known length at middleware entry.
			request.Header.Set("Content-Type", "application/json")

			response := httptest.NewRecorder()
			requireJSONBody()(http.HandlerFunc(handler.updateDarkMode)).ServeHTTP(response, request)

			if response.Code != test.want {
				t.Errorf("status = %d, want %d; body: %s", response.Code, test.want, response.Body)
			}

			stored, err := settings.GetAll(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			wantMode := "system"
			if test.want == http.StatusOK {
				wantMode = "dark"
			}

			if stored.DarkMode != wantMode {
				t.Errorf("stored mode = %s, want %s", stored.DarkMode, wantMode)
			}
		})
	}
}
