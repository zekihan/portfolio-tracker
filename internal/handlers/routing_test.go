package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestRoutingPreservesExistingHTTPBehavior(t *testing.T) {
	t.Chdir("../..")

	router := New(nil, nil, nil, nil).SetupRoutes()

	stylesheet, err := os.ReadFile("web/static/style.css")
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		method, path, contentType, location, body string
		status                                    int
		securityHeaders                           bool
	}{
		{
			method: http.MethodGet, path: "/missing", status: 404,
			contentType: "text/plain", body: "404 page not found", securityHeaders: true,
		},
		{method: http.MethodGet, path: "/holdings/", status: 301, location: "/holdings"},
		{
			method: http.MethodPost, path: "/api/holdings/?currency=USD", status: 307,
			location: "/api/holdings?currency=USD",
		},
		{
			method: http.MethodHead, path: "/holdings", status: 404,
			contentType: "text/plain", body: "404 page not found", securityHeaders: true,
		},
		{
			method: http.MethodOptions, path: "/api/settings", status: 404,
			contentType: "text/plain", body: "404 page not found", securityHeaders: true,
		},
		{
			method: http.MethodGet, path: "/api/holdings/.", status: 400,
			contentType: "application/json; charset=utf-8", body: `{"error":"invalid id"}`, securityHeaders: true,
		},
		{
			method: http.MethodGet, path: "/api/holdings/..", status: 400,
			contentType: "application/json; charset=utf-8", body: `{"error":"invalid id"}`, securityHeaders: true,
		},
		{
			method: http.MethodGet, path: "/api/holdings/1%2F2", status: 404,
			contentType: "text/plain", body: "404 page not found", securityHeaders: true,
		},
		{
			method: http.MethodGet, path: "/holdings/../settings", status: 404,
			contentType: "text/plain", body: "404 page not found", securityHeaders: true,
		},
		{method: http.MethodGet, path: "/static", status: 301, location: "/static/"},
		{method: http.MethodGet, path: "/static/nope", status: 404, securityHeaders: true},
		{
			method: http.MethodGet, path: "/static/../style.css", status: 200,
			contentType: "text/css; charset=utf-8", body: string(stylesheet), securityHeaders: true,
		},
		{
			method: http.MethodGet, path: "/static//style.css", status: 200,
			contentType: "text/css; charset=utf-8", body: string(stylesheet), securityHeaders: true,
		},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(
				t.Context(),
				test.method,
				test.path,
				http.NoBody,
			)
			router.ServeHTTP(response, request)

			if response.Code != test.status {
				t.Errorf("status = %d, want %d", response.Code, test.status)
			}

			if test.contentType != "" && response.Header().Get("Content-Type") != test.contentType {
				t.Errorf(
					"content type = %q, want %q",
					response.Header().Get("Content-Type"),
					test.contentType,
				)
			}

			if response.Header().Get("Location") != test.location {
				t.Errorf("location = %q, want %q", response.Header().Get("Location"), test.location)
			}

			if test.location == "" && response.Body.String() != test.body {
				t.Errorf("unexpected response body: %.100s", response.Body.String())
			}

			if got := response.Header().
				Get("X-Content-Type-Options") ==
				"nosniff"; got != test.securityHeaders {
				t.Errorf("security headers present = %t, want %t", got, test.securityHeaders)
			}
		})
	}

	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/static/", http.NoBody),
	)

	if response.Code != http.StatusNotFound ||
		strings.Contains(response.Body.String(), "style.css") {
		t.Error("static directory must preserve the empty listing and 404 status")
	}
}

func TestRedirectPreservesForwardedPrefix(t *testing.T) {
	t.Parallel()

	router := http.NewServeMux()
	router.HandleFunc("GET /holdings", func(http.ResponseWriter, *http.Request) {})
	router.HandleFunc("/", notFound)
	endpoint := compatibleRoutes(router, http.NotFoundHandler())
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/holdings/?currency=USD", http.NoBody)
	request.Header.Set("X-Forwarded-Prefix", "/tracker")

	response := httptest.NewRecorder()
	endpoint.ServeHTTP(response, request)

	if response.Code != http.StatusMovedPermanently ||
		response.Header().Get("Location") != "/tracker/holdings?currency=USD" {
		t.Errorf("redirect = %d %q", response.Code, response.Header().Get("Location"))
	}
}

func TestRecoveryReturnsAnEmptyServerError(t *testing.T) {
	t.Parallel()

	endpoint := recoverRequests(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("private panic detail")
	}))
	response := httptest.NewRecorder()
	endpoint.ServeHTTP(
		response,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody),
	)

	if response.Code != http.StatusInternalServerError || response.Body.Len() != 0 {
		t.Errorf("response = %d %q, want empty 500", response.Code, response.Body.String())
	}
}
