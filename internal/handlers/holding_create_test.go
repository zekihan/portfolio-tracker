package handlers

import (
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

const idempotentHoldingBody = `{"name":"Cash","asset_type":"Cash","base_currency":"TRY","quantity":5}`

func TestHoldingCreateIdempotencyHTTP(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	handler := New(services.NewHoldingService(database, nil), nil, nil, nil)
	router := requirePortfolioGeneration(http.HandlerFunc(handler.createHolding))
	send := func(body, generation string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/api/holdings",
			strings.NewReader(body),
		)
		request.Header.Set("Idempotency-Key", "64b77137-52b8-4840-932d-72e67e60b190")
		request.Header.Set(portfolioGenerationHeader, generation)

		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)

		return response
	}

	original := send(idempotentHoldingBody, "0")
	if original.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", original.Code, original.Body)
	}
	// Later edits must neither change the receipt nor be overwritten by a retry.
	_, err = database.ExecContext(t.Context(), "UPDATE holdings SET quantity = 9")
	if err != nil {
		t.Fatal(err)
	}

	retry := send(`{"quantity":5,"base_currency":"TRY","asset_type":"Cash","name":"Cash",
 "current_price":999,"current_currency":"USD","price_fetched_at":"2000-01-01T00:00:00Z",
 "price_source":"Unstored quote","is_stale":true,"is_failed":true,"price_error":"Unstored error"}`, "0")
	if retry.Code != http.StatusCreated || retry.Body.String() != original.Body.String() {
		t.Fatalf("retry = %d %s, original = %s", retry.Code, retry.Body, original.Body)
	}

	changed := send(strings.Replace(idempotentHoldingBody, `"quantity":5`, `"quantity":7`, 1), "0")
	if changed.Code != http.StatusUnprocessableEntity {
		t.Fatalf("changed payload = %d %s", changed.Code, changed.Body)
	}

	var total int

	err = database.GetContext(
		t.Context(),
		&total,
		"SELECT SUM(quantity) FROM holdings",
	)
	if err != nil ||
		total != 9 {
		t.Fatalf("retry changed stored holdings: total=%d err=%v", total, err)
	}

	_, err = database.ExecContext(t.Context(), "UPDATE snapshot_state SET generation = 1")
	if err != nil {
		t.Fatal(err)
	}

	stale := send(idempotentHoldingBody, "0")
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale replay = %d %s", stale.Code, stale.Body)
	}
}

func TestHoldingCreateResponseMatchesStoredHolding(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		key    string
		manual string
	}{
		{name: "no override or key", manual: `{"Float64":999,"Valid":false}`},
		{name: "override and receipt", key: "cash-creation", manual: `{"Float64":12.5,"Valid":true}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			holdings := services.NewHoldingService(database, nil)
			handler := New(holdings, nil, nil, nil)
			router := requirePortfolioGeneration(http.HandlerFunc(handler.createHolding))
			body := `{"name":"Cash","asset_type":"Cash","base_currency":"TRY","quantity":5,
 "current_price":999,"current_currency":"USD","valuation_available":true,"manual_price":` + test.manual + "}"
			request := httptest.NewRequestWithContext(
				t.Context(), http.MethodPost, "/api/holdings", strings.NewReader(body),
			)
			request.Header.Set(portfolioGenerationHeader, "0")

			if test.key != "" {
				request.Header.Set("Idempotency-Key", test.key)
			}

			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusCreated {
				t.Fatalf("create = %d %s", response.Code, response.Body)
			}

			var created models.Holding

			err = json.Unmarshal(response.Body.Bytes(), &created)
			if err != nil {
				t.Fatal(err)
			}

			stored, err := holdings.GetByID(t.Context(), created.ID)
			if err != nil || stored == nil {
				t.Fatalf("stored holding = %+v, %v", stored, err)
			}

			encoded, err := json.Marshal(stored)
			if err != nil {
				t.Fatal(err)
			}

			if response.Body.String() != string(encoded) {
				t.Errorf("create response = %s, stored holding = %s", response.Body, encoded)
			}
		})
	}
}

func TestHoldingCreateIdempotencyKeyValidation(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		keys   []string
		status int
	}{
		{name: "missing", status: http.StatusCreated},
		{name: "empty", keys: []string{""}, status: http.StatusBadRequest},
		{name: "duplicate", keys: []string{"same", "same"}, status: http.StatusBadRequest},
		{name: "space", keys: []string{"bad key"}, status: http.StatusBadRequest},
		{name: "unicode", keys: []string{"clé"}, status: http.StatusBadRequest},
		{name: "long", keys: []string{strings.Repeat("a", 129)}, status: http.StatusBadRequest},
		{name: "boundary", keys: []string{strings.Repeat("a", 128)}, status: http.StatusCreated},
		{name: "random hex", keys: []string{"0123456789abcdef0123456789abcdef"}, status: http.StatusCreated},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			handler := New(services.NewHoldingService(database, nil), nil, nil, nil)

			request := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				"/api/holdings",
				strings.NewReader(idempotentHoldingBody),
			)
			for _, key := range test.keys {
				request.Header.Add("Idempotency-Key", key)
			}

			response := httptest.NewRecorder()
			handler.createHolding(response, request)

			if response.Code != test.status {
				t.Fatalf("status = %d, expected %d: %s", response.Code, test.status, response.Body)
			}
		})
	}
}
