package handlers

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

func TestProviderMappingRequiresExplicitFields(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		`{}`, `null`, `{"provider":""}`, `{"instrumentId":""}`,
		`{"provider":null,"instrumentId":""}`, `{"provider":"","instrumentId":null}`,
		`{"provider":"","instrumentId":""}`,
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			_, err = database.ExecContext(
				t.Context(),
				`INSERT INTO holdings (id, name, asset_type, base_currency, price_provider, instrument_id)
 VALUES (1, 'fixture', 'Stock', 'TRY', 'Manual', '100')`,
			)
			if err != nil {
				t.Fatal(err)
			}

			prices := price.NewService(database, price.DefaultRegistry())
			handler := New(nil, nil, nil, prices)
			request := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPut,
				"/api/holdings/1/provider",
				strings.NewReader(body),
			)
			request.SetPathValue("id", "1")

			response := httptest.NewRecorder()
			handler.updateHoldingProvider(response, request)

			wantStatus := http.StatusBadRequest
			wantProvider := "Manual"
			wantInstrument := "100"

			if body == `{"provider":"","instrumentId":""}` {
				wantStatus = http.StatusOK
				wantProvider = ""
				wantInstrument = ""
			}

			if response.Code != wantStatus {
				t.Errorf("status = %d, want %d", response.Code, wantStatus)
			}

			var provider, instrument string

			err = database.QueryRowContext(t.Context(), `SELECT price_provider, instrument_id FROM holdings WHERE id = 1`).
				Scan(&provider, &instrument)
			if err != nil {
				t.Fatal(err)
			}

			if provider != wantProvider || instrument != wantInstrument {
				t.Errorf(
					"mapping = %q %q, want %q %q",
					provider,
					instrument,
					wantProvider,
					wantInstrument,
				)
			}
		})
	}
}
