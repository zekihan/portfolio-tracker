package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

func TestProviderUpdateRespectsCancellation(t *testing.T) {
	t.Parallel()

	for _, canceled := range []bool{true, false} {
		name := "active"
		if canceled {
			name = "canceled"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
				(id, name, asset_type, price_provider, instrument_id)
				VALUES (1, 'Stock', 'Stock', 'Yahoo Finance', 'OLD')`)
			if err != nil {
				t.Fatal(err)
			}

			handler := New(nil, nil, nil, price.NewService(database, price.DefaultRegistry()))
			router := http.NewServeMux()
			router.HandleFunc("PUT /provider/{id}", handler.updateHoldingProvider)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			if canceled {
				cancel()
			}

			request := httptest.NewRequestWithContext(ctx, http.MethodPut, "/provider/1",
				strings.NewReader(`{"provider":"Finnhub","instrumentId":"NEW"}`))
			request.Header.Set("Content-Type", "application/json")

			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			wantStatus, wantProvider, wantInstrument := http.StatusOK, "Finnhub", "NEW"
			if canceled {
				wantStatus, wantProvider, wantInstrument = http.StatusInternalServerError, "Yahoo Finance", "OLD"
			}

			if response.Code != wantStatus {
				t.Errorf("status = %d, want %d", response.Code, wantStatus)
			}

			var provider, instrument string

			err = database.QueryRowContext(
				t.Context(),
				"SELECT price_provider, instrument_id FROM holdings WHERE id = 1",
			).Scan(&provider, &instrument)
			if err != nil {
				t.Fatal(err)
			}

			if provider != wantProvider || instrument != wantInstrument {
				t.Errorf(
					"stored mapping = %s/%s, want %s/%s",
					provider,
					instrument,
					wantProvider,
					wantInstrument,
				)
			}
		})
	}
}
