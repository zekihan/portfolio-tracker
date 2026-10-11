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

func TestPageRoutesRenderTheirOwnContent(t *testing.T) {
	t.Chdir("../..")

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.ExecContext(t.Context(), `INSERT INTO price_cache
  (provider, instrument_id, price, currency, source, expires_at, last_error)
  VALUES ('Frankfurter', 'USD', 40, 'TRY', 'test', ?, '')`, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	prices := price.NewService(database, price.DefaultRegistry())
	holdings := services.NewHoldingService(database, prices)
	handler := New(holdings, services.NewSnapshotService(database, prices, holdings),
		services.NewSettingsService(database), prices)
	router := handler.SetupRoutes()

	for _, test := range []struct {
		path   string
		marker string
	}{
		{path: "/", marker: `class="dashboard"`},
		{path: "/holdings", marker: `class="holdings-page"`},
		{path: "/history", marker: `class="history-page"`},
		{path: "/sources", marker: `class="sources-page"`},
		{path: "/settings", marker: `class="settings-page"`},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, test.path, nil)
			router.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", response.Code)
			}

			body := response.Body.String()
			if !strings.Contains(body, test.marker) {
				t.Errorf("missing page-specific content %s", test.marker)
			}

			if !strings.Contains(body, "</html>") {
				t.Error("page rendering stopped before completing shared layout")
			}

			if !strings.Contains(body, "<!DOCTYPE html>") {
				t.Error("missing shared layout")
			}
		})
	}

	_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
		(name, asset_type, base_currency, quantity, est_price, manual_price,
		 ticker, price_provider, instrument_id, basket, location, notes)
		VALUES ('Example', 'Cash', 'TRY', 2, 200, 100, '', '', '', '', '', '')`)
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	router.ServeHTTP(response,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	if !strings.Contains(response.Body.String(), "100% (1/1)") ||
		!strings.Contains(response.Body.String(), "</html>") {
		t.Error("populated dashboard did not render coverage and complete layout")
	}

	checkPopulatedHoldingCurrency(t, router)
}

func checkPopulatedHoldingCurrency(t *testing.T, router http.Handler) {
	t.Helper()

	for _, test := range []struct {
		currency string
		total    string
		estimate string
	}{
		{currency: "TRY", total: "₺ 200.00", estimate: "₺ 400.00"},
		{currency: "USD", total: "$ 5.00", estimate: "$ 10.00"},
	} {
		t.Run("populated holdings "+test.currency, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(),
				http.MethodGet, "/holdings?currency="+test.currency, nil))

			body := response.Body.String()
			if response.Code != http.StatusOK || !strings.Contains(body, "</html>") {
				t.Error("populated holdings did not render complete document")
			}

			for _, value := range []string{test.total, test.estimate} {
				if strings.Count(body, value) < 2 {
					t.Errorf("missing desktop/mobile selected-currency value %q", value)
				}
			}

			if strings.Contains(body, "$ 200.00") || strings.Contains(body, "$ 400.00") {
				t.Error("TRY amounts mislabeled as USD")
			}
		})
	}
}
