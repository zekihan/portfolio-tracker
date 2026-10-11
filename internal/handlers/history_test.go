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

func TestSnapshotPaginationValidation(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	handler := New(nil, services.NewSnapshotService(database, nil, nil), nil, nil)
	router := gin.New()
	router.GET("/snapshots", handler.getSnapshots)

	tests := []struct {
		query  string
		status int
	}{
		{query: "?limit=-1", status: http.StatusBadRequest},
		{query: "?limit=0", status: http.StatusBadRequest},
		{query: "?limit=1001", status: http.StatusBadRequest},
		{query: "?offset=-1", status: http.StatusBadRequest},
		{query: "?limit=abc", status: http.StatusBadRequest},
		{query: "?offset=abc", status: http.StatusBadRequest},
		{query: "", status: http.StatusOK},
		{query: "?limit=1&offset=0", status: http.StatusOK},
		{query: "?limit=1000&offset=10", status: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.query, func(t *testing.T) {
			t.Parallel()

			response := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
				"/snapshots"+test.query, http.NoBody)
			router.ServeHTTP(response, request)

			if response.Code != test.status {
				t.Errorf("status = %d, want %d; body: %s",
					response.Code, test.status, response.Body.String())
			}
		})
	}
}
