package handlers

import (
	"encoding/json"
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

func TestEuroCurrencyPagesAndAPIs(t *testing.T) {
	t.Chdir("../..")

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.ExecContext(t.Context(), `INSERT INTO price_cache
 (provider, instrument_id, price, currency, source, expires_at, last_error)
 VALUES ('Frankfurter', 'USD', 40, 'TRY', 'test', ?, ''),
 ('Frankfurter', 'EUR', 50, 'TRY', 'test', ?, '')`, time.Now().UTC().Add(time.Hour), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
 (name, asset_type, base_currency, quantity, est_price, manual_price, price_provider)
 VALUES ('Euro cash', 'Cash', 'EUR', 5, 2, 1, 'Manual')`)
	if err != nil {
		t.Fatal(err)
	}

	prices := price.NewService(database, price.DefaultRegistry())
	holdings := services.NewHoldingService(database, prices)
	settings := services.NewSettingsService(database)
	handler := New(
		holdings,
		services.NewSnapshotService(database, prices, holdings),
		settings,
		prices,
	)
	router := handler.SetupRoutes()

	for _, test := range []struct{ path, marker string }{
		{"/?currency=EUR", "€ 5.00"},
		{"/?currency=EUR", "€ 10.00"},
		{"/?currency=EUR", "1 EUR=50.00 TRY"},
		{"/holdings?currency=EUR", "€ 5.00"},
		{"/holdings?currency=USD", "$ 6.25"},
		{"/holdings?currency=TRY", "₺ 250.00"},
		{"/history?currency=EUR", `value="EUR" selected`},
		{"/partials/holding-form/1", `value="EUR" selected`},
		{"/partials/manual-price/1", "Price (EUR)"},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(
			response,
			httptest.NewRequestWithContext(t.Context(), http.MethodGet, test.path, nil),
		)

		if response.Code != http.StatusOK ||
			!strings.Contains(response.Body.String(), test.marker) {
			t.Errorf("%s: status %d, missing %q", test.path, response.Code, test.marker)
		}
	}

	checkEuroCurrencyAPIs(t, router, handler, settings)
}

func checkEuroCurrencyAPIs(
	t *testing.T,
	router http.Handler,
	handler *Handler,
	settings *services.SettingsService,
) {
	t.Helper()

	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			"/api/dashboard?currency=EUR",
			nil,
		),
	)

	var data map[string]json.RawMessage

	err := json.Unmarshal(response.Body.Bytes(), &data)
	if err != nil {
		t.Fatal(err)
	}

	if response.Code != http.StatusOK || string(data["CurrentValueEUR"]) != "5" ||
		string(data["EstimatedValueEUR"]) != "10" ||
		string(data["AllocationByType"]) != `{"Cash":5}` {
		t.Errorf("EUR dashboard response = %d %s", response.Code, response.Body)
	}

	response = httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			"/api/holdings?currency=EUR",
			nil,
		),
	)

	var holdings []map[string]json.RawMessage

	err = json.Unmarshal(response.Body.Bytes(), &holdings)
	if err != nil {
		t.Fatal(err)
	}

	if response.Code != http.StatusOK || len(holdings) != 1 ||
		string(holdings[0]["NormTotalEUR"]) != "5" {
		t.Errorf("EUR holdings response = %d %s", response.Code, response.Body)
	}

	checkEuroSettingsAPI(t, router, handler, settings)
}

func checkEuroSettingsAPI(
	t *testing.T,
	router http.Handler,
	handler *Handler,
	settings *services.SettingsService,
) {
	t.Helper()

	response := httptest.NewRecorder()
	handler.updateCurrency(response, httptest.NewRequestWithContext(t.Context(), http.MethodPatch,
		"/api/settings/currency", strings.NewReader(`{"currency":"EUR"}`)))

	saved, err := settings.GetAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if response.Code != http.StatusOK || saved.DisplayCurrency != "EUR" {
		t.Errorf("EUR settings response = %d, stored = %q", response.Code, saved.DisplayCurrency)
	}

	response = httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/settings", nil),
	)

	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `value="EUR" selected`) {
		t.Error("settings did not render saved EUR option")
	}
}
