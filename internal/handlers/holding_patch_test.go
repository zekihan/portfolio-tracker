package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

func TestHoldingPartialUpdates(t *testing.T) {
	t.Chdir("../..")

	for _, test := range []struct {
		apply  func(*models.Holding)
		name   string
		body   string
		id     string
		status int
		cancel bool
	}{
		{func(holding *models.Holding) { holding.Name = "New name" }, "name", `{"name":"New name"}`, "1", 204, false},
		{func(holding *models.Holding) { holding.Ticker = "NEW" }, "ticker", `{"ticker":"NEW"}`, "1", 204, false},
		{nil, "empty name", `{"name":""}`, "1", 400, false},
		{func(holding *models.Holding) { holding.Notes = "New notes" }, "notes", `{"notes":"New notes"}`, "1", 204, false},
		{func(holding *models.Holding) { holding.Basket = "Long term" }, "basket", `{"basket":"Long term"}`, "1", 204, false},
		{func(holding *models.Holding) {
			holding.Location = "New location"
		}, "location", `{"location":"New location"}`, "1", 204, false},
		{func(holding *models.Holding) { holding.Quantity = 12.5 }, "quantity", `{"quantity":12.5}`, "1", 204, false},
		{func(holding *models.Holding) { holding.EstPrice = 90 }, "estimate", `{"est_price":90}`, "1", 204, false},
		{func(holding *models.Holding) {
			holding.Notes = ""
			holding.Basket = ""
			holding.Location = ""
		}, "clear text", `{"notes":"","basket":"","location":""}`, "1", 204, false},
		{func(holding *models.Holding) {
			holding.Quantity = 0
			holding.EstPrice = 0
		}, "zero", `{"quantity":0,"est_price":0}`, "1", 204, false},
		{nil, "empty", `{}`, "1", 400, false},
		{nil, "null", `{"notes":null}`, "1", 400, false},
		{nil, "unsupported", `{"asset_type":"Wrong"}`, "1", 400, false},
		{nil, "negative quantity", `{"quantity":-1}`, "1", 400, false},
		{nil, "negative estimate", `{"est_price":-1}`, "1", 400, false},
		{nil, "invalid batch", `{"notes":"Do not save","est_price":-1}`, "1", 400, false},
		{nil, "wrong type", `{"notes":123}`, "1", 400, false},
		{nil, "malformed", `{`, "1", 400, false},
		{nil, "missing", `{"notes":"New"}`, "999", 404, false},
		{nil, "invalid id", `{"notes":"New"}`, "invalid", 400, false},
		{nil, "canceled", `{"notes":"New"}`, "1", 500, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			holdings := services.NewHoldingService(database, nil)

			var holding models.Holding

			holding.Name = "Original"
			holding.Ticker = "ORIG"
			holding.AssetType = models.AssetTypeStock
			holding.BaseCurrency = "TRY"
			holding.PriceProvider = "Manual"
			holding.InstrumentID = "100"
			holding.Quantity = 2
			holding.EstPrice = 200
			holding.Basket = "Basket"
			holding.Location = "Location"

			holding.Notes = "Notes"

			err = holdings.Create(t.Context(), &holding)
			if err != nil {
				t.Fatal(err)
			}

			before, err := holdings.GetByID(t.Context(), holding.ID)
			if err != nil {
				t.Fatal(err)
			}

			want := *before
			if test.apply != nil {
				test.apply(&want)
			}

			id := test.id
			if id == "" {
				id = "1"
			}

			ctx := t.Context()

			if test.cancel {
				var cancel context.CancelFunc

				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			request := httptest.NewRequestWithContext(
				ctx,
				http.MethodPatch,
				"/api/holdings/"+id,
				strings.NewReader(test.body),
			)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(portfolioGenerationHeader, "0")

			response := httptest.NewRecorder()
			New(holdings, nil, nil, nil).SetupRoutes().ServeHTTP(response, request)

			if response.Code != test.status {
				t.Errorf(
					"status=%d want=%d body=%s",
					response.Code,
					test.status,
					response.Body.String(),
				)
			}

			stored, err := holdings.GetByID(t.Context(), holding.ID)
			if err != nil {
				t.Fatal(err)
			}

			stored.UpdatedAt = want.UpdatedAt
			if !reflect.DeepEqual(*stored, want) {
				t.Errorf("stored=%+v want=%+v", *stored, want)
			}
		})
	}
}
