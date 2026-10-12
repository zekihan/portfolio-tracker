package handlers

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/price"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

func TestHoldingMutationsReportMissingRows(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	prices := price.NewService(database, price.DefaultRegistry())
	handler := New(services.NewHoldingService(database, prices), nil, nil, prices)

	tests := []struct {
		handle http.HandlerFunc
		name   string
		body   string
	}{
		{
			name:   "update",
			body:   `{"name":"Missing","asset_type":"Stock","quantity":1,"base_currency":"TRY","price_provider":"Manual"}`,
			handle: handler.updateHolding,
		},
		{name: "estimate", body: `{"estPrice":1}`, handle: handler.updateEstPrice},
		{name: "manual", body: `{"price":1}`, handle: handler.updateManualPrice},
		{name: "clear manual", body: "", handle: handler.clearManualPrice},
		{name: "delete", body: "", handle: handler.deleteHolding},
		{
			name:   "provider",
			body:   `{"provider":"Manual","instrumentId":"100"}`,
			handle: handler.updateHoldingProvider,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			request := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPut,
				"/holdings/999",
				strings.NewReader(test.body),
			)
			request.SetPathValue("id", "999")

			response := httptest.NewRecorder()
			test.handle(response, request)

			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body)
			}
		})
	}
}

func TestHoldingWritesRejectUnknownProvider(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.ExecContext(
		t.Context(),
		`INSERT INTO holdings (id, name, asset_type, base_currency, price_provider)
 VALUES (1, 'Original', 'Stock', 'TRY', 'Manual')`,
	)
	if err != nil {
		t.Fatal(err)
	}

	prices := price.NewService(database, price.DefaultRegistry())

	handler := New(services.NewHoldingService(database, prices), nil, nil, prices)
	for _, test := range []struct {
		handle http.HandlerFunc
		name   string
		body   string
	}{
		{
			name: "create",
			body: `{"name":"Invalid","asset_type":"Stock","quantity":1,
 "base_currency":"TRY","price_provider":"Unknown"}`,
			handle: handler.createHolding,
		},
		{
			name: "update",
			body: `{"name":"Invalid","asset_type":"Stock","quantity":1,
 "base_currency":"TRY","price_provider":"Unknown"}`,
			handle: handler.updateHolding,
		},
		{name: "mapping", body: `{"provider":"Unknown","instrumentId":"100"}`, handle: handler.updateHoldingProvider},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			request := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPut,
				"/holdings/1",
				strings.NewReader(test.body),
			)
			request.SetPathValue("id", "1")

			response := httptest.NewRecorder()
			test.handle(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body)
			}

			var count int

			err := database.GetContext(
				t.Context(),
				&count,
				`SELECT COUNT(*) FROM holdings WHERE name='Original' AND price_provider='Manual'`,
			)
			if err != nil {
				t.Fatal(err)
			}

			if count != 1 {
				t.Fatal("invalid provider changed existing holding")
			}

			err = database.GetContext(
				t.Context(),
				&count,
				`SELECT COUNT(*) FROM holdings`,
			)
			if err != nil {
				t.Fatal(err)
			}

			if count != 1 {
				t.Fatal("invalid provider created holding")
			}
		})
	}
}

func TestAPIDisplayCurrencyValidation(t *testing.T) {
	t.Parallel()

	handler := New(nil, nil, nil, nil)
	for _, handle := range []http.HandlerFunc{handler.getHoldings, handler.getDashboard, handler.getAllocation} {
		for _, currency := range []string{"EUR", "", "usd", "JPY"} {
			request := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodGet,
				"/?currency="+currency,
				http.NoBody,
			)
			response := httptest.NewRecorder()
			handle(response, request)

			if response.Code != http.StatusBadRequest {
				t.Errorf("currency=%q: status = %d", currency, response.Code)
			}
		}
	}

	for _, query := range []string{"", "?currency=TRY", "?currency=USD"} {
		request := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			"/"+query,
			http.NoBody,
		)

		currency, valid := apiDisplayCurrency(httptest.NewRecorder(), request)
		if !valid || (currency != "TRY" && currency != "USD") {
			t.Errorf("valid currency rejected: %s", query)
		}
	}
}
