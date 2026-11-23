package handlers

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	commonsmetrics "github.com/zekihan/go-commons/metrics"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/price"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

// TestMiddlewareRecordsMuxPattern serves a request through the full production
// chain (registry.Middleware around SetupRoutes, as in cmd/server/main.go) and
// checks the recorded route is the mux pattern, not "unknown". The generation
// and cached-price middlewares replace the request with a copy, so the mux
// must be wrapped with metrics.CapturePattern for the pattern to arrive.
func TestMiddlewareRecordsMuxPattern(t *testing.T) {
	t.Chdir("../..")

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	prices := price.NewService(database, price.DefaultRegistry())
	holdings := services.NewHoldingService(database, prices)
	handler := New(holdings, services.NewSnapshotService(database, prices, holdings),
		services.NewSettingsService(database), prices)

	registry := commonsmetrics.New()
	chain := registry.Middleware(handler.SetupRoutes())

	response := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/api/holdings/999999",
		http.NoBody,
	)
	chain.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("holding status = %d, want 404", response.Code)
	}

	recorder := httptest.NewRecorder()
	registry.Handler().ServeHTTP(
		recorder,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", http.NoBody),
	)

	body := recorder.Body.String()

	if !strings.Contains(
		body,
		`http_requests_total{method="GET",route="/api/holdings/{id}",status="404"}`,
	) {
		t.Fatalf("metrics exposition missing mux pattern route:\n%s", body)
	}

	if strings.Contains(body, `route="unknown"`) {
		t.Fatalf("metrics exposition labels a request unknown:\n%s", body)
	}
}
