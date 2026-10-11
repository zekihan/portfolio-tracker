package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

func TestProviderMappingReadRespectsCancellation(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		canceled bool
	}{
		{name: "canceled", canceled: true},
		{name: "live", canceled: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			canceled := test.canceled

			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
    (id, name, asset_type, price_provider, instrument_id)
    VALUES (1, 'Stock', 'Stock', 'Yahoo Finance', 'TEST')`)
			if err != nil {
				t.Fatal(err)
			}

			handler := New(nil, nil, nil, price.NewService(database, price.DefaultRegistry()))
			router := gin.New()
			router.GET("/mapping", handler.getProviderMapping)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			if canceled {
				cancel()
			}

			response := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/mapping", http.NoBody)
			router.ServeHTTP(response, request)

			if canceled {
				if response.Code != http.StatusInternalServerError {
					t.Errorf("status = %d, want 500", response.Code)
				}

				return
			}

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", response.Code)
			}

			var mappings []price.HoldingProviderMapping

			err = json.Unmarshal(response.Body.Bytes(), &mappings)
			if err != nil {
				t.Fatal(err)
			}

			if len(mappings) != 1 {
				t.Fatalf("mapping count = %d, want 1", len(mappings))
			}

			want := price.HoldingProviderMapping{
				HoldingID: 1, HoldingName: "Stock", Provider: "Yahoo Finance", InstrumentID: "TEST",
			}
			if mappings[0] != want {
				t.Errorf("unexpected mapping: %+v", mappings[0])
			}
		})
	}
}
