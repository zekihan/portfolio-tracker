package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

func TestRenderPageKeepsValuationCurrencyAfterSettingsChange(t *testing.T) {
	t.Chdir("../..")

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	settings := services.NewSettingsService(database)
	handler := New(nil, nil, settings, nil)
	handler.SetupRoutes()

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	displayCurrency := handler.pageCurrency(request)
	valuation := &models.DashboardData{
		ValuationComplete: true,
		CurrentValueTRY:   200,
		CurrentValueUSD:   5,
		USDTRYRate:        40,
		TotalHoldings:     1,
		ValuedCount:       1,
	}

	// Another request changes the preference between valuation and rendering.
	err = settings.SetDisplayCurrency(t.Context(), currencyEUR)
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.renderPage(response, request, "dashboard.html", map[string]any{
		"Title":           titleDashboard,
		"DisplayCurrency": displayCurrency,
		"Data":            valuation,
	})

	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `data-currency="TRY"`) {
		t.Errorf("dashboard status %d, want 200 with TRY display currency", response.Code)
	}

	_, value, _ := strings.Cut(body, `id="current-value-try">`)

	value, _, _ = strings.Cut(value, "</div>")
	if got := strings.TrimSpace(value); got != "₺ 200.00" {
		t.Errorf("dashboard primary value = %q, want ₺ 200.00", got)
	}
}
