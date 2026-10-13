package handlers

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

func TestJSONMutationRejectsTrailingData(t *testing.T) {
	t.Parallel()

	for _, suffix := range []string{` {}`, ` garbage`, strings.Repeat(" ", maxRequestBodyBytes)} {
		t.Run("suffix length "+strconv.Itoa(len(suffix)), func(t *testing.T) {
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
				http.MethodPatch,
				"/api/settings/currency",
				strings.NewReader(`{"currency":"USD"}`+suffix),
			)
			request.ContentLength = -1
			request.Header.Set("Content-Type", "application/json")

			response := httptest.NewRecorder()
			requireJSONBody()(http.HandlerFunc(handler.updateCurrency)).ServeHTTP(response, request)

			if response.Code < 400 {
				t.Errorf("malformed mutation accepted: %d", response.Code)
			}

			stored, err := settings.GetAll(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			if stored.DisplayCurrency != "TRY" {
				t.Errorf("malformed mutation changed currency: %s", stored.DisplayCurrency)
			}
		})
	}
}

func TestDynamicResponsesAreNotStored(t *testing.T) {
	t.Parallel()

	router := http.NewServeMux()
	router.HandleFunc(
		"GET /api/settings/backup",
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) },
	)
	router.HandleFunc(
		"GET /holdings",
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) },
	)
	router.HandleFunc("/", notFound)
	handler := compatibleRoutes(
		router,
		http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) },
		),
	)

	for _, target := range []string{"/api/settings/backup", "/holdings", "/missing", "/static/style.css"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(
			response,
			httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil),
		)

		got := response.Header().Get("Cache-Control")
		if target == "/static/style.css" {
			if got != "" {
				t.Errorf("static cache unexpectedly disabled: %s", got)
			}

			continue
		}

		if got != "no-store" {
			t.Errorf("%s Cache-Control = %q", target, got)
		}
	}
}

func TestStaticFilesDoNotExposePrivateFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "backup.db")

	err := os.WriteFile(outside, []byte("private financial data"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(outside, filepath.Join(root, "backup.db"))
	if err != nil {
		t.Fatal(err)
	}

	err = os.Mkdir(filepath.Join(root, ".private"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Mkdir(filepath.Join(root, "nested"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{".env", ".private/config", "nested/public.css", "public.css"} {
		err = os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	handler := staticFiles(root)

	for _, name := range []string{"backup.db", ".env", ".private/config", "nested/public.css", "public.css"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(
			response,
			httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/static/"+name, nil),
		)

		want := http.StatusNotFound
		if name == "public.css" || name == "nested/public.css" {
			want = http.StatusOK
		}

		if response.Code != want {
			t.Errorf("%s status = %d, want %d", name, response.Code, want)
		}
	}
}

func TestTemplateFailureDoesNotCommitPartialSuccess(t *testing.T) {
	t.Parallel()

	page := template.Must(template.New("broken").Parse(`private prefix {{.Missing.Value}}`))
	renderer := &pageRenderer{pages: map[string]*template.Template{"broken": page}}
	response := httptest.NewRecorder()
	renderer.render(response, http.StatusOK, "broken", struct{ Missing string }{Missing: "value"})

	if response.Code != http.StatusInternalServerError {
		t.Errorf("status = %d", response.Code)
	}

	if strings.Contains(response.Body.String(), "private prefix") {
		t.Error("leaked partial template output")
	}
}

func TestTemplateSuccessPreservesStatus(t *testing.T) {
	t.Parallel()

	page := template.Must(template.New("unavailable").Parse(`complete unavailable page`))
	renderer := &pageRenderer{pages: map[string]*template.Template{"unavailable": page}}
	response := httptest.NewRecorder()
	renderer.render(response, http.StatusServiceUnavailable, "unavailable", nil)

	if response.Code != http.StatusServiceUnavailable ||
		response.Body.String() != "complete unavailable page" {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestStaticFilesRejectBackslashPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := os.Mkdir(filepath.Join(root, "nested"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	// On Windows this resolves to .env; on Unix it is a literal filename.
	// Both platforms must reject the HTTP path before touching the file.
	//nolint:gocritic // A platform-dependent path is required to exercise the bypass on Unix and Windows.
	err = os.WriteFile(filepath.Join(root, `nested\..\.env`), []byte("private fixture"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	handler := staticFiles(root)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/static/nested%5c..%5c.env", nil,
	))

	if response.Code != http.StatusNotFound {
		t.Fatalf("backslash path returned %d: %s", response.Code, response.Body.String())
	}
}
