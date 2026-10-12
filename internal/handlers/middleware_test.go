package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequireJSONBody(t *testing.T) {
	t.Parallel()

	oversized := `{"name":"` + strings.Repeat("a", maxRequestBodyBytes) + `"}`

	for _, test := range []struct {
		name        string
		contentType string
		body        string
		status      int
	}{
		{name: "json", contentType: "application/json", body: `{}`, status: http.StatusOK},
		{name: "json with charset", contentType: "application/json; charset=utf-8", body: `{}`, status: http.StatusOK},
		{name: "text plain", contentType: "text/plain", body: `{}`, status: http.StatusUnsupportedMediaType},
		{
			name:        "form",
			contentType: "application/x-www-form-urlencoded",
			body:        `a=b`,
			status:      http.StatusUnsupportedMediaType,
		},
		{name: "missing content type", contentType: "", body: `{}`, status: http.StatusUnsupportedMediaType},
		{name: "oversized", contentType: "application/json", body: oversized, status: http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			router := http.NewServeMux()
			router.HandleFunc("POST /x", func(writer http.ResponseWriter, request *http.Request,
			) {
				var payload map[string]any

				err := decodeJSON(request, &payload)
				if err != nil {
					writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: err.Error()})

					return
				}

				writer.WriteHeader(http.StatusOK)
			})

			request := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				"/x",
				strings.NewReader(test.body),
			)
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}

			response := httptest.NewRecorder()
			requireJSONBody()(router).ServeHTTP(response, request)

			if response.Code != test.status {
				t.Errorf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}

func TestRequireJSONBodyAllowsBodylessRequests(t *testing.T) {
	t.Parallel()

	router := http.NewServeMux()
	router.HandleFunc("DELETE /x", func(writer http.ResponseWriter, request *http.Request,
	) {
		writer.WriteHeader(http.StatusOK)
	})

	request := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/x", http.NoBody)
	response := httptest.NewRecorder()
	requireJSONBody()(router).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestBrowserOriginProtection(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		method string
		origin string
		site   string
		status int
	}{
		{"cross-site bodyless refresh", http.MethodPost, "https://attacker.example", "cross-site", 403},
		{"cross-site delete", http.MethodDelete, "https://attacker.example", "cross-site", 403},
		{"cross-origin legacy browser", http.MethodPost, "https://attacker.example", "", 403},
		{"same-site different host", http.MethodPost, "https://other.example.com", "same-site", 403},
		{"same-origin browser", http.MethodPost, "http://example.com", "same-origin", 204},
		{"same-origin legacy browser", http.MethodPost, "http://example.com", "", 204},
		{"non-browser client", http.MethodPost, "", "", 204},
		{"navigation", http.MethodGet, "https://attacker.example", "cross-site", 204},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			router := http.NewServeMux()
			router.HandleFunc(
				test.method+" /write",
				func(writer http.ResponseWriter, request *http.Request,
				) {
					writer.WriteHeader(http.StatusNoContent)
				},
			)
			request := httptest.NewRequestWithContext(
				t.Context(),
				test.method,
				"http://example.com/write",
				http.NoBody,
			)
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Sec-Fetch-Site", test.site)

			response := httptest.NewRecorder()
			protectBrowserRequests()(router).ServeHTTP(response, request)

			if response.Code != test.status {
				t.Errorf("status = %d, want %d", response.Code, test.status)
			}

			if response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Error("missing MIME protection")
			}
		})
	}
}
