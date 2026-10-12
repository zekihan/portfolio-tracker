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

func TestEstimatedPriceRejectsNegativeUpdates(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		body   string
		status int
		stored float64
	}{
		{name: "negative", body: `{"estPrice":-1}`, status: http.StatusBadRequest, stored: 20},
		{name: "zero", body: `{"estPrice":0}`, status: http.StatusOK, stored: 0},
		{name: "positive", body: `{"estPrice":12.5}`, status: http.StatusOK, stored: 12.5},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
    (id, name, asset_type, est_price) VALUES (1, 'Stock', 'Stock', 20)`)
			if err != nil {
				t.Fatal(err)
			}

			handler := New(services.NewHoldingService(database, nil), nil, nil, nil)
			router := http.NewServeMux()
			router.HandleFunc("PUT /estimate/{id}", handler.updateEstPrice)

			request := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/estimate/1",
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
				"SELECT est_price FROM holdings WHERE id = 1",
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
