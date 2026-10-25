package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

type boundaryGetter struct {
	calls *atomic.Int64
	name  string
}

func (getter *boundaryGetter) Name() string       { return getter.name }
func (getter *boundaryGetter) Type() string       { return "Stock/ETF" }
func (getter *boundaryGetter) TTL() time.Duration { return time.Minute }
func (getter *boundaryGetter) GetPrice(context.Context, string) (models.Price, error) {
	getter.calls.Add(1)

	return models.Price{Value: 40, Currency: "TRY", Source: getter.name, FetchedAt: time.Now()}, nil
}

func TestGETValuationsNeverStartProviderWork(t *testing.T) {
	t.Chdir("../..")

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
 (name, asset_type, base_currency, quantity, price_provider, instrument_id)
 VALUES ('Synthetic stock', 'Stock', 'TRY', 2, 'Yahoo Finance', 'TEST')`)
	if err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int64

	registry := price.NewRegistry()
	registry.Register(&boundaryGetter{name: "Yahoo Finance", calls: &calls})
	registry.Register(&boundaryGetter{name: "Frankfurter", calls: &calls})
	prices := price.NewService(database, registry)

	t.Cleanup(func() { _ = prices.Close() })

	holdings := services.NewHoldingService(database, prices)
	router := New(
		holdings,
		services.NewSnapshotService(database, prices, holdings),
		services.NewSettingsService(database),
		prices,
	).SetupRoutes()

	for _, cached := range []bool{false, true} {
		if cached {
			_, err = database.ExecContext(t.Context(), `INSERT INTO price_cache
   (provider, instrument_id, price, currency, source, expires_at, last_error) VALUES
   ('Yahoo Finance', 'TEST', 20, 'TRY', 'fixture', ?, ''),
   ('Frankfurter', 'USD', 40, 'TRY', 'fixture', ?, ''),
   ('Frankfurter', 'EUR', 50, 'TRY', 'fixture', ?, '')`,
				time.Now().Add(-time.Hour), time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
			if err != nil {
				t.Fatal(err)
			}
		}

		for range 3 {
			for _, path := range []string{
				"/", "/holdings", "/api/dashboard",
				"/api/dashboard/allocation", "/api/holdings", "/api/holdings/1",
			} {
				request := httptest.NewRequestWithContext(
					t.Context(),
					http.MethodGet,
					path+"?currency=EUR",
					nil,
				)
				request.Header.Set("Sec-Fetch-Site", "cross-site")
				request.Header.Set("Origin", "https://attacker.example")

				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)

				if response.Code >= 500 && cached {
					t.Fatalf("cached read %s: %d %s", path, response.Code, response.Body.String())
				}
			}
		}

		if calls.Load() != 0 {
			t.Fatalf("GET started %d provider calls", calls.Load())
		}
	}

	request := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/api/sources/refresh",
		nil,
	)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("X-Portfolio-Action", "1")
	request.Header.Set(portfolioGenerationHeader, "0")

	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK || calls.Load() == 0 {
		t.Fatalf(
			"explicit refresh: %d, calls=%d, body=%s",
			response.Code,
			calls.Load(),
			response.Body.String(),
		)
	}
}

func TestWorkRoutesRequirePOSTAndActionHeader(t *testing.T) {
	t.Chdir("../..")

	router := New(nil, nil, nil, nil).SetupRoutes()

	for _, path := range []string{"/api/settings/backup", "/api/sources/refresh", "/api/sources/test"} {
		for _, test := range []struct {
			method, site, origin, header string
			status                       int
		}{
			{http.MethodGet, "cross-site", "https://attacker.example", "", http.StatusNotFound},
			{http.MethodHead, "cross-site", "https://attacker.example", "", http.StatusNotFound},
			{http.MethodPost, "cross-site", "https://attacker.example", "1", http.StatusForbidden},
			{http.MethodPost, "same-site", "https://other.example.com", "1", http.StatusForbidden},
			{http.MethodPost, "", "https://attacker.example", "1", http.StatusForbidden},
			{http.MethodPost, "same-origin", "http://example.com", "", http.StatusForbidden},
			{http.MethodPost, "", "", "", http.StatusForbidden},
		} {
			request := httptest.NewRequestWithContext(
				t.Context(),
				test.method,
				"http://example.com"+path,
				nil,
			)
			request.Header.Set("Sec-Fetch-Site", test.site)
			request.Header.Set("Origin", test.origin)
			request.Header.Set("X-Portfolio-Action", test.header)

			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != test.status {
				t.Errorf(
					"%s %s site=%s: %d, want %d",
					test.method,
					path,
					test.site,
					response.Code,
					test.status,
				)
			}
		}
	}
}

func TestBackupPOSTAndConcurrentAdmission(t *testing.T) {
	t.Chdir("../..")

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	handler := New(nil, nil, services.NewSettingsService(database), nil)
	router := handler.SetupRoutes()

	for _, busy := range []bool{false, true} {
		if busy {
			handler.backupMu.Lock()
		}

		request := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/api/settings/backup",
			nil,
		)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		request.Header.Set("X-Portfolio-Action", "1")

		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)

		if busy {
			handler.backupMu.Unlock()

			if response.Code != http.StatusTooManyRequests {
				t.Fatalf("busy backup: %d", response.Code)
			}
		} else if response.Code != http.StatusOK || !strings.HasPrefix(response.Body.String(), "SQLite format 3\x00") {
			t.Fatalf("backup POST: %d %s", response.Code, response.Body.String())
		}
	}
}
