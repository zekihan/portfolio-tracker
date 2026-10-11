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

func TestManualPriceRejectsNegativeUpdates(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		body   string
		status int
		stored float64
	}{
		{name: "negative", body: `{"price":-1}`, status: http.StatusBadRequest, stored: 20},
		{name: "zero", body: `{"price":0}`, status: http.StatusOK, stored: 0},
		{name: "positive", body: `{"price":12.5}`, status: http.StatusOK, stored: 12.5},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
    (id, name, asset_type, manual_price) VALUES (1, 'Stock', 'Stock', 20)`)
			if err != nil {
				t.Fatal(err)
			}

			handler := New(services.NewHoldingService(database, nil), nil, nil, nil)
			router := gin.New()
			router.PUT("/manual/:id", handler.updateManualPrice)

			request := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/manual/1",
				strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")

			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != test.status {
				t.Errorf("status = %d, want %d", response.Code, test.status)
			}

			var stored float64

			err = database.GetContext(
				t.Context(),
				&stored,
				"SELECT manual_price FROM holdings WHERE id = 1",
			)
			if err != nil {
				t.Fatal(err)
			}

			if stored != test.stored {
				t.Errorf("stored price = %v, want %v", stored, test.stored)
			}
		})
	}
}
