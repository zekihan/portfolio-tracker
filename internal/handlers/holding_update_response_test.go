package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

func TestUpdateHoldingResponseMatchesStoredAccounting(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		`{"name":"Edited","asset_type":"Cash","base_currency":"TRY","quantity":3}`,
		`{"name":"Edited","asset_type":"Cash","base_currency":"TRY","quantity":3,
 "manual_price":{"Float64":999,"Valid":true},
 "current_price":999,"current_currency":"USD",
 "valuation_available":true,"created_at":"2000-01-01T00:00:00Z"}`,
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			holdings := services.NewHoldingService(database, nil)
			holding := models.Holding{
				Name:         "Original",
				AssetType:    models.AssetTypeCash,
				BaseCurrency: "TRY",
				Quantity:     2,
				ManualPrice:  sql.NullFloat64{Float64: 20, Valid: true},
			}

			err = holdings.Create(t.Context(), &holding)
			if err != nil {
				t.Fatal(err)
			}

			request := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPut,
				"/api/holdings/1",
				strings.NewReader(body),
			)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(portfolioGenerationHeader, "0")

			response := httptest.NewRecorder()
			handler := New(holdings, nil, nil, nil)
			router := http.NewServeMux()
			router.Handle(
				"PUT /api/holdings/{id}",
				requirePortfolioGeneration(http.HandlerFunc(handler.updateHolding)),
			)
			router.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}

			var got models.Holding

			err = json.Unmarshal(response.Body.Bytes(), &got)
			if err != nil {
				t.Fatal(err)
			}

			stored, err := holdings.GetByID(t.Context(), holding.ID)
			if err != nil {
				t.Fatal(err)
			}

			encoded, err := json.Marshal(stored)
			if err != nil {
				t.Fatal(err)
			}

			if strings.TrimSpace(response.Body.String()) != string(encoded) {
				t.Errorf("response differs from committed holding: got=%+v stored=%+v", got, stored)
			}

			if got.TotalValue() != 60 {
				t.Errorf("response value=%v, want retained override value 60", got.TotalValue())
			}
		})
	}
}
