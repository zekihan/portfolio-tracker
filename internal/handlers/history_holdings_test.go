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

func TestHistoryPageListsHoldingsWithoutCachedPrices(t *testing.T) {
	t.Chdir("../..")

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
 (id, name, asset_type, quantity, basket, location)
 VALUES (7, 'Cash reserve', 'Cash', 10, 'Income', 'Broker');
 INSERT INTO snapshots (total_value_try, total_value_usd, usd_try_rate, holdings_json)
 VALUES (400, 10, 40, '[{"id":7,"value_try":400,"value_usd":10}]')`)
	if err != nil {
		t.Fatal(err)
	}

	router := New(
		services.NewHoldingService(database, nil),
		services.NewSnapshotService(database, nil, nil),
		services.NewSettingsService(database),
		nil,
	).SetupRoutes()

	for _, test := range []struct{ path, marker string }{
		{"/api/history/holding/7", `"value_usd":10`},
		{"/history", `<option value="7" data-basket="Income" data-location="Broker">Cash reserve</option>`},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			test.path,
			http.NoBody,
		)
		router.ServeHTTP(response, request)

		if response.Code != http.StatusOK ||
			!strings.Contains(response.Body.String(), test.marker) {
			t.Errorf("GET %s returned %d, want 200 with %q", test.path, response.Code, test.marker)
		}
	}
}
