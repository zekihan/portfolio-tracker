package handlers

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

func TestSnapshotDeletionRoutes(t *testing.T) {
	t.Chdir("../..")

	tests := []struct {
		name         string
		path         string
		confirmation []string
		crossOrigin  bool
		status       int
		remaining    int
	}{
		{name: "individual", path: "/1", status: http.StatusNoContent, remaining: 1},
		{name: "missing", path: "/99", status: http.StatusNotFound, remaining: 2},
		{name: "invalid", path: "/abc", status: http.StatusBadRequest, remaining: 2},
		{name: "zero", path: "/0", status: http.StatusBadRequest, remaining: 2},
		{name: "negative", path: "/-1", status: http.StatusBadRequest, remaining: 2},
		{
			name:      "overflow",
			path:      "/999999999999999999999",
			status:    http.StatusBadRequest,
			remaining: 2,
		},
		{name: "all", confirmation: []string{"clear-history"}, status: http.StatusNoContent},
		{
			name:         "all ignores filter",
			path:         "?days=1&limit=1&offset=1",
			confirmation: []string{"clear-history"},
			status:       http.StatusNoContent,
		},
		{name: "unconfirmed", status: http.StatusBadRequest, remaining: 2},
		{
			name:         "wrong confirmation",
			confirmation: []string{"yes"},
			status:       http.StatusBadRequest,
			remaining:    2,
		},
		{
			name:         "duplicate confirmation",
			confirmation: []string{"clear-history", "clear-history"},
			status:       http.StatusBadRequest,
			remaining:    2,
		},
		{
			name:        "cross origin individual",
			path:        "/1",
			crossOrigin: true,
			status:      http.StatusForbidden,
			remaining:   2,
		},
		{
			name:         "cross origin all",
			confirmation: []string{"clear-history"},
			crossOrigin:  true,
			status:       http.StatusForbidden,
			remaining:    2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			_, err = database.ExecContext(t.Context(), `
    INSERT INTO holdings (name, asset_type) VALUES ('Current', 'Stock');
    INSERT INTO snapshots (id, total_value_try, total_value_usd, usd_try_rate, holdings_json, timestamp)
     VALUES (1, 1, 1, 1, '[]', datetime('now', '-40 days')), (2, 2, 2, 1, '[]', CURRENT_TIMESTAMP);`)
			if err != nil {
				t.Fatal(err)
			}

			handler := New(nil, services.NewSnapshotService(database, nil, nil), nil, nil)
			router := handler.SetupRoutes()

			request := httptest.NewRequestWithContext(t.Context(), http.MethodDelete,
				"/api/history/snapshots"+test.path, http.NoBody)
			for _, value := range test.confirmation {
				request.Header.Add("X-Clear-History-Confirm", value)
			}

			if test.crossOrigin {
				request.Header.Set("Origin", "https://attacker.example")
			}

			request.Header.Set(portfolioGenerationHeader, "0")

			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != test.status {
				t.Fatalf(
					"status = %d, want %d: %s",
					response.Code,
					test.status,
					response.Body.String(),
				)
			}

			if response.Code == http.StatusNoContent && response.Body.Len() != 0 {
				t.Error("204 has response body")
			}

			assertSnapshotDeletionCounts(t, database, test.remaining)
		})
	}
}

func assertSnapshotDeletionCounts(t *testing.T, database *sqlx.DB, remaining int) {
	t.Helper()

	for table, want := range map[string]int{"snapshots": remaining, "holdings": 1} {
		var count int

		err := database.GetContext(t.Context(), &count, "SELECT COUNT(*) FROM "+table)
		if err != nil {
			t.Fatal(err)
		}

		if count != want {
			t.Errorf("%s count = %d, want %d", table, count, want)
		}
	}
}
