package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

func TestStaleValuationPagesShowQuoteTimes(t *testing.T) {
	t.Chdir("../..")

	handler := New(nil, nil, nil, nil)
	_ = handler.SetupRoutes()
	quoteTime := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	holdingData := models.Holding{
		Name: "Cached", BaseCurrency: "TRY", CurrentCurrency: "TRY", Quantity: 1,
		CurrentPrice: 100, ValuationAvailable: true, IsStale: true, PriceFetchedAt: quoteTime,
	}
	holding := models.HoldingWithPrice{Holding: holdingData, NormTotalTRY: 100, NormPriceTRY: 100}

	for _, page := range []string{"holdings.html", "dashboard.html"} {
		t.Run(page, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.renderer.render(response, http.StatusOK, page, map[string]any{
				"DisplayCurrency": "TRY", "Holdings": []models.HoldingWithPrice{holding},
				"Data": &models.DashboardData{
					ValuationComplete: true, ValuedCount: 1, TotalHoldings: 1,
					CurrentValueTRY: 100, USDTRYRate: 40, StaleCount: 1,
					LastUpdated: quoteTime.Add(time.Hour), FXIsStale: true, FXFetchedAt: quoteTime,
				},
			})

			body := response.Body.String()
			if !strings.Contains(body, formatTime(quoteTime)) || !strings.Contains(body, "Stale") ||
				!strings.Contains(body, "</html>") || strings.Contains(body, ">Updated ") {
				t.Error(
					"stale valuation should show quote time, stale label, and no fresh update claim",
				)
			}
		})
	}
}
