package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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

			router := gin.New()
			router.Use(requireJSONBody())
			router.POST("/x", func(ctx *gin.Context) {
				var payload map[string]any

				err := ctx.ShouldBindJSON(&payload)
				if err != nil {
					ctx.JSON(http.StatusBadRequest, gin.H{errorKey: err.Error()})

					return
				}

				ctx.Status(http.StatusOK)
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
			router.ServeHTTP(response, request)

			if response.Code != test.status {
				t.Errorf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}

func TestRequireJSONBodyAllowsBodylessRequests(t *testing.T) {
	t.Parallel()

	router := gin.New()
	router.Use(requireJSONBody())
	router.DELETE("/x", func(ctx *gin.Context) { ctx.Status(http.StatusOK) })

	request := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/x", http.NoBody)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", response.Code, http.StatusOK)
	}
}
