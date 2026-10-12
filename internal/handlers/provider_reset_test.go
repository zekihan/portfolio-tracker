package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

var errResetProbeUnavailable = errors.New("provider unavailable")

type resetProbeProvider struct{ calls int }

func (provider *resetProbeProvider) Name() string       { return "Reset probe" }
func (provider *resetProbeProvider) Type() string       { return "Test" }
func (provider *resetProbeProvider) TTL() time.Duration { return time.Minute }
func (provider *resetProbeProvider) GetPrice(context.Context, string) (models.Price, error) {
	provider.calls++

	return models.Price{}, errResetProbeUnavailable
}

func TestDataReplacementResetsProviderFailuresOnlyOnSuccess(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, operation string
		succeed         bool
	}{
		{name: "clear/success", operation: "clear", succeed: true},
		{name: "clear/failure", operation: "clear", succeed: false},
		{name: "restore/success", operation: "restore", succeed: true},
		{name: "restore/failure", operation: "restore", succeed: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			getter := &resetProbeProvider{}
			registry := price.NewRegistry()
			registry.Register(getter)
			prices := price.NewService(database, registry)
			settings := services.NewSettingsService(database)

			handler := New(nil, nil, settings, prices)
			for range 4 {
				_, _ = prices.GetPrice(t.Context(), getter.Name(), "item")
			}

			if getter.calls != 3 {
				t.Fatalf("breaker did not open: calls=%d", getter.calls)
			}

			response := httptest.NewRecorder()

			applyReplacementForResetTest(t, handler, response, test.operation, test.succeed)

			if test.succeed && response.Code != http.StatusOK {
				t.Fatalf("replacement failed: %d %s", response.Code, response.Body)
			}

			if !test.succeed && response.Code < 400 {
				t.Fatalf("replacement unexpectedly succeeded: %d", response.Code)
			}

			_, _ = prices.GetPrice(t.Context(), getter.Name(), "item")

			wantCalls := 3
			if test.succeed {
				wantCalls = 4
			}

			if getter.calls != wantCalls {
				t.Fatalf("provider calls=%d, want %d", getter.calls, wantCalls)
			}
		})
	}
}

func applyReplacementForResetTest(
	t *testing.T,
	handler *Handler,
	response *httptest.ResponseRecorder,
	operation string,
	succeed bool,
) {
	t.Helper()

	if operation == "clear" {
		request := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodDelete,
			"/clear",
			http.NoBody,
		)
		if !succeed {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			request = request.WithContext(ctx)
		}

		handler.clearAllData(response, request)

		return
	}

	content := []byte("not a SQLite backup")

	if succeed {
		path := filepath.Join(t.TempDir(), "backup.db")

		err := handler.settingsSvc.Backup(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}

		content, err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}

	handler.restoreBackup(response, restoreRequest(t, "backup.db", content, true))
}
