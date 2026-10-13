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

func TestPartialValuationPages(t *testing.T) {
	t.Chdir("../..")
	router := partialValuationRouter(t)

	for _, test := range []struct {
		path      string
		required  []string
		forbidden []string
	}{
		{
			path: "/?currency=USD",
			required: []string{
				"Partial portfolio valuation", "Partial Current Value", "$ 5.00",
				"2 of 3 holdings valued; 1 unavailable", "Partial Allocation",
				"Comparison unavailable", "</html>",
			},
			forbidden: []string{"Portfolio valuation unavailable", "Diff:", "secret-upstream"},
		},
		{
			path: "/holdings?currency=USD",
			required: []string{
				"Partial holding valuations", "Unavailable", "$ 5.00", "$ 0.00",
				`data-edit-value="Unsupported"`, `hx-get="/partials/holding-form/3"`, "</html>",
			},
			forbidden: []string{"secret-upstream", "unsupported quote currency"},
		},
		{
			path:     "/api/dashboard/allocation?currency=USD",
			required: []string{`"valuation_complete":false`, `"valued_count":2`, `"unvalued_count":1`},
		},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response,
				httptest.NewRequestWithContext(t.Context(), http.MethodGet, test.path, nil))

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", response.Code)
			}

			body := response.Body.String()
			for _, value := range test.required {
				if !strings.Contains(body, value) {
					t.Errorf("missing %q", value)
				}
			}

			for _, value := range test.forbidden {
				if strings.Contains(body, value) {
					t.Errorf("unexpected %q", value)
				}
			}
		})
	}
}

func TestAllUnpricedDashboardHasNoFalseZero(t *testing.T) {
	t.Chdir("../..")
	router := unavailableValuationRouter(t, "unsupported quote currency")
	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil),
	)

	body := response.Body.String()

	if response.Code != http.StatusOK || !strings.Contains(body, "Partial portfolio valuation") ||
		!strings.Contains(body, "Unavailable") || strings.Contains(body, "₺ 0.00") {
		t.Error(
			"all-unpriced portfolio should show partial/unavailable, not zero or a global failure",
		)
	}
}

func partialValuationRouter(t *testing.T) http.Handler {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
 (name, asset_type, base_currency, quantity, est_price, manual_price,
 ticker, price_provider, instrument_id, basket, location, notes) VALUES
 ('Healthy', 'Stock', 'TRY', 2, 200, 100, 'HEALTHY', '', '', '', '', ''),
 ('Zero quantity', 'Stock', 'TRY', 0, 200, 100, 'ZERO', '', '', '', '', ''),
 ('Unsupported', 'Stock', 'TRY', 2, 200, 0, 'TEST', 'Yahoo Finance', 'TEST', '', '', '')`)
	if err != nil {
		t.Fatal(err)
	}

	expires := time.Now().UTC().Add(time.Hour)

	_, err = database.ExecContext(t.Context(), `INSERT INTO price_cache
 (provider, instrument_id, price, currency, source, expires_at, last_error) VALUES
 ('Frankfurter', 'USD', 40, 'TRY', 'test', ?, ''),
 ('Yahoo Finance', 'TEST', 100, 'GBP', 'test', ?, 'secret-upstream')`, expires, expires)
	if err != nil {
		t.Fatal(err)
	}

	prices := price.NewService(database, price.DefaultRegistry())
	holdings := services.NewHoldingService(database, prices)

	return New(holdings, services.NewSnapshotService(database, prices, holdings),
		services.NewSettingsService(database), prices).SetupRoutes()
}
