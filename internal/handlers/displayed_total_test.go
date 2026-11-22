package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

func TestDashboardDisplaysRoundedZeroGainAndLargeTotals(t *testing.T) {
	t.Chdir("../..")

	handler := New(nil, nil, nil, nil)
	_ = handler.SetupRoutes()

	dust := renderDashboardTotals(t, handler, "EUR", &models.DashboardData{
		ValuationComplete:          true,
		EstimatedValuationComplete: true,
		ValuedCount:                1,
		TotalHoldings:              1,
		CurrentValueEUR:            10,
		EstimatedValueEUR:          10 - 1e-12,
		CurrentValueTRY:            500,
		USDTRYRate:                 40,
		EURTRYRate:                 50,
	})
	if strings.Contains(dust, "-0.00") {
		t.Fatalf("negative dust gain rendered as a loss: %s", dust)
	}

	if !strings.Contains(dust, "Diff: € 0.00") || !strings.Contains(dust, "€ 10.00") {
		t.Fatalf("euro gain display = %s", dust)
	}

	large := renderDashboardTotals(t, handler, "TRY", &models.DashboardData{
		ValuationComplete:          true,
		EstimatedValuationComplete: true,
		ValuedCount:                1,
		TotalHoldings:              1,
		CurrentValueTRY:            1e12 + 0.25,
		EstimatedValueTRY:          1e12 + 0.25,
		USDTRYRate:                 40,
	})
	if !strings.Contains(large, "₺ 1,000,000,000,000.25") || strings.Contains(large, "-0.00") {
		t.Fatalf("large total display = %s", large)
	}
}

func renderDashboardTotals(
	t *testing.T,
	handler *Handler,
	currency string,
	data *models.DashboardData,
) string {
	t.Helper()

	response := httptest.NewRecorder()
	handler.renderer.render(response, http.StatusOK, "dashboard.html", map[string]any{
		"DisplayCurrency": currency,
		"Title":           titleDashboard,
		"Data":            data,
	})

	if response.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d", response.Code)
	}

	return strings.Join(strings.Fields(response.Body.String()), " ")
}
