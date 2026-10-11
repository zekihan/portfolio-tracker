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

func TestHistoryDayRangeValidation(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	handler := New(nil, services.NewSnapshotService(database, nil, nil), nil, nil)
	router := gin.New()
	router.GET("/chart", handler.getChartData)
	router.GET("/holding/:id", handler.getHoldingHistory)

	for _, path := range []string{"/chart", "/holding/1"} {
		for _, test := range []struct {
			query  string
			status int
		}{
			{query: "?days=-1", status: http.StatusBadRequest},
			{query: "?days=0", status: http.StatusBadRequest},
			{query: "?days=36501", status: http.StatusBadRequest},
			{query: "?days=9223372036854775807", status: http.StatusBadRequest},
			{query: "?days=abc", status: http.StatusBadRequest},
			{query: "", status: http.StatusOK},
			{query: "?days=1", status: http.StatusOK},
			{query: "?days=36500", status: http.StatusOK},
		} {
			t.Run(path+test.query, func(t *testing.T) {
				t.Parallel()

				response := httptest.NewRecorder()
				request := httptest.NewRequestWithContext(
					t.Context(),
					http.MethodGet,
					path+test.query,
					http.NoBody,
				)
				router.ServeHTTP(response, request)

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
}
