package handlers

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/price"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

func TestZeroQuantityUnquotedHoldingPages(t *testing.T) {
	t.Chdir("../..")

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
 (name, asset_type, quantity, price_provider, base_currency, manual_price)
 VALUES ('Cash', 'Cash', 100, 'Manual', 'USD', 1),
 ('Closed', 'Stock', 0, 'Manual', 'USD', 0)`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = database.ExecContext(t.Context(), `INSERT INTO price_cache
 (provider, instrument_id, price, currency, source, expires_at, last_error)
 VALUES ('Frankfurter', 'USD', 40, 'TRY', 'test', ?, '')`, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	prices := price.NewService(database, price.DefaultRegistry())

	t.Cleanup(func() { _ = prices.Close() })

	holdings := services.NewHoldingService(database, prices)
	router := New(
		holdings,
		services.NewSnapshotService(database, prices, holdings),
		services.NewSettingsService(database),
		prices,
	).SetupRoutes()

	for _, path := range []string{"/", "/holdings"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(
			response,
			httptest.NewRequestWithContext(t.Context(), http.MethodGet, path+"?currency=USD", nil),
		)

		body := response.Body.String()
		if response.Code != http.StatusOK ||
			strings.Contains(body, "Partial portfolio valuation") ||
			strings.Contains(body, "Partial holding valuations") ||
			!strings.Contains(body, "$ 100.00") {
			t.Fatalf("%s incorrectly incomplete: status %d", path, response.Code)
		}

		if path == "/holdings" &&
			(!strings.Contains(body, "Unavailable") || !strings.Contains(body, "Closed")) {
			t.Fatal("missing quote no longer visible on closed holding")
		}
	}
}
