package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

func TestProviderHealthReadRespectsCancellation(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		canceled bool
		status   int
	}{
		{name: "canceled", canceled: true, status: http.StatusInternalServerError},
		{name: "live", canceled: false, status: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			handler := New(nil, nil, nil, price.NewService(database, price.DefaultRegistry()))
			router := http.NewServeMux()
			router.HandleFunc("GET /providers", handler.getProviders)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			if test.canceled {
				cancel()
			}

			request := httptest.NewRequestWithContext(
				ctx,
				http.MethodGet,
				"/providers",
				http.NoBody,
			)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}

			if test.canceled {
				return
			}

			var providers []map[string]json.RawMessage

			err = json.Unmarshal(response.Body.Bytes(), &providers)
			if err != nil {
				t.Fatal(err)
			}

			if len(providers) == 0 {
				t.Error("live request returned no provider health entries")
			}
		})
	}
}
