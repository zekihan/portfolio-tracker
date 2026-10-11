package handlers

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

func TestCreateHoldingRejectsNegativeQuantity(t *testing.T) {
	t.Parallel()

	const base = `"name":"Stock","asset_type":"Stock","base_currency":"USD",`

	for _, test := range []struct {
		name   string
		body   string
		status int
	}{
		{name: "negative", body: `{` + base + `"quantity":-1}`, status: http.StatusBadRequest},
		{name: "zero", body: `{` + base + `"quantity":0}`, status: http.StatusCreated},
		{name: "positive", body: `{` + base + `"quantity":2.5}`, status: http.StatusCreated},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			handler := New(services.NewHoldingService(database, nil), nil, nil, nil)
			router := gin.New()
			router.POST("/api/holdings", handler.createHolding)

			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/holdings",
				strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")

			response := httptest.NewRecorder()
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
