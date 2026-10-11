package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// errDBDetail stands in for a driver error that must never reach clients.
var errDBDetail = errors.New("pq: relation \"holdings\" does not exist")

func TestRespondInternalErrorHidesDetails(t *testing.T) {
	t.Parallel()

	router := gin.New()
	router.GET("/x", func(ctx *gin.Context) {
		respondInternalError(ctx, errDBDetail)
	})

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", http.NoBody)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}

	if strings.Contains(response.Body.String(), "holdings") {
		t.Errorf("response leaks internal error: %s", response.Body.String())
	}

	if !strings.Contains(response.Body.String(), internalErrorMsg) {
		t.Errorf("response = %s, want generic message", response.Body.String())
	}
}
