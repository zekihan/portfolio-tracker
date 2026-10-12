package handlers

import (
	"bytes"
	"log/slog"
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

func TestValuationPagesUnavailable(t *testing.T) {
	t.Chdir("../..")

	for _, failure := range []string{"FX unavailable", "database unavailable"} {
		t.Run(failure, func(t *testing.T) {
			router := unavailableValuationRouter(t, failure)

			var logs bytes.Buffer

			previousLogger := slog.Default()

			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previousLogger) })

			for _, path := range []string{"/", "/holdings"} {
				response := httptest.NewRecorder()
				router.ServeHTTP(
					response,
					httptest.NewRequestWithContext(t.Context(), http.MethodGet,
						path+"?currency=USD", nil),
				)

				checkUnavailableValuationResponse(t, path, response)
				body := response.Body.String()

				if path == "/holdings" && failure != "database unavailable" {
					for _, metadata := range []string{
						"Example holding", "Example basket", "Example location",
						"Example notes", `data-field="name"`, `hx-get="/partials/holding-form/1"`,
					} {
						if !strings.Contains(body, metadata) {
							t.Errorf("missing editable metadata %q", metadata)
						}
					}
				}
			}

			if !strings.Contains(logs.String(), "Dashboard valuation unavailable") ||
				!strings.Contains(logs.String(), "Holdings valuation unavailable") {
				t.Error("page failures were not logged")
			}
		})
	}
}

func unavailableValuationRouter(t *testing.T, failure string) http.Handler {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
		(name, asset_type, base_currency, quantity, est_price, manual_price,
		 ticker, price_provider, instrument_id, basket, location, notes)
		VALUES ('Example holding', 'Stock', 'TRY', 2, 200, 0, 'TEST',
		 'Yahoo Finance', 'TEST', 'Example basket', 'Example location', 'Example notes')`)
	if err != nil {
		t.Fatal(err)
	}

	registry := price.NewRegistry() // No network access for unavailable-FX tests.
	if failure == "unsupported quote currency" {
		registry = price.DefaultRegistry()
		expires := time.Now().UTC().Add(time.Hour)

		_, err = database.ExecContext(t.Context(), `INSERT INTO price_cache
			(provider, instrument_id, price, currency, source, expires_at, last_error) VALUES
			('Frankfurter', 'USD', 40, 'TRY', 'test', ?, ''),
			('Yahoo Finance', 'TEST', 100, 'EUR', 'test', ?, '')`, expires, expires)
		if err != nil {
			t.Fatal(err)
		}
	}

	prices := price.NewService(database, registry)
	holdings := services.NewHoldingService(database, prices)
	handler := New(holdings, services.NewSnapshotService(database, prices, holdings),
		services.NewSettingsService(database), prices)
	router := handler.SetupRoutes()

	if failure == "database unavailable" {
		err = database.Close()
		if err != nil {
			t.Fatal(err)
		}
	}

	return router
}

func checkUnavailableValuationResponse(
	t *testing.T, path string, response *httptest.ResponseRecorder,
) {
	t.Helper()

	body := response.Body.String()
	if response.Code != http.StatusServiceUnavailable {
		t.Errorf("%s status = %d, want 503", path, response.Code)
	}

	for _, required := range []string{
		`role="alert"`, "unavailable", ">Retry</a>",
		`href="` + path + `?currency=USD"`, "</html>",
	} {
		if !strings.Contains(body, required) {
			t.Errorf("%s missing %q", path, required)
		}
	}

	for _, forbidden := range []string{
		"No holdings yet", "$ 0.00", "₺ 0.00",
		"Estimated total covers", `id="current-value-try"`, `id="allocation-chart"`,
		"unsupported quote currency", "database is closed", "not found",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf(
				"%s contains misleading value or internal detail %q",
				path,
				forbidden,
			)
		}
	}
}

func TestValuationPagesTrulyEmptyPortfolio(t *testing.T) {
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

	for _, test := range []struct{ path, marker string }{
		{path: "/", marker: "₺ 0.00"},
		{path: "/holdings", marker: "No holdings yet"},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(
			response,
			httptest.NewRequestWithContext(t.Context(), http.MethodGet, test.path, nil),
		)

		body := response.Body.String()
		if response.Code != http.StatusOK || !strings.Contains(body, test.marker) ||
			!strings.Contains(body, "</html>") || strings.Contains(body, "unavailable") {
			t.Errorf("%s did not render a successful empty portfolio", test.path)
		}
	}
}
