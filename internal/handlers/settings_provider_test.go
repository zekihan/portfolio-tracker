package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

type probeProvider struct{}

func (probeProvider) Name() string       { return "Probe" }
func (probeProvider) Type() string       { return "Test" }
func (probeProvider) TTL() time.Duration { return time.Minute }
func (probeProvider) GetPrice(_ context.Context, instrument string) (models.Price, error) {
	return models.Price{
		Value:     12.5,
		Currency:  "EUR",
		Source:    instrument,
		FetchedAt: time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC),
	}, nil
}

func TestProviderEndpoint(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	registry := price.NewRegistry()
	registry.Register(probeProvider{})
	prices := price.NewService(database, registry)

	t.Cleanup(func() { _ = prices.Close() })

	handler := New(nil, nil, nil, prices)

	for _, tc := range []struct {
		name, body string
		status     int
		success    bool
	}{
		{"success", `{"provider":"Probe","instrumentId":"ABC"}`, http.StatusOK, true},
		{"unknown", `{"provider":"missing","instrumentId":"ABC"}`, http.StatusOK, false},
		{"malformed", `{`, http.StatusBadRequest, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			request := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				"/",
				strings.NewReader(tc.body),
			)
			response := httptest.NewRecorder()
			handler.testProvider(response, request)

			if response.Code != tc.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body)
			}

			var got map[string]any

			err := json.Unmarshal(response.Body.Bytes(), &got)
			if err != nil {
				t.Fatal(err)
			}

			if tc.success {
				if got["success"] != true || got["price"] != 12.5 || got["currency"] != "EUR" ||
					got["source"] != "ABC" ||
					got["fetched_at"] != "2024-01-02T03:04:05Z" {
					t.Fatalf("response = %v", got)
				}
			} else if got["error"] == nil || (tc.status == http.StatusOK && got["success"] != false) {
				t.Fatalf("response = %v", got)
			}
		})
	}
}

func TestGetSettingsEndpoint(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	settings := services.NewSettingsService(database)

	err = settings.SetDisplayCurrency(t.Context(), "EUR")
	if err != nil {
		t.Fatal(err)
	}

	err = settings.SetDarkMode(t.Context(), "dark")
	if err != nil {
		t.Fatal(err)
	}

	err = settings.SetRefreshInterval(t.Context(), 120)
	if err != nil {
		t.Fatal(err)
	}

	handler := New(nil, nil, settings, nil)
	response := httptest.NewRecorder()
	handler.getSettings(
		response,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody),
	)

	var got models.Settings

	err = json.Unmarshal(response.Body.Bytes(), &got)
	if err != nil {
		t.Fatal(err)
	}

	if response.Code != http.StatusOK || got.DisplayCurrency != "EUR" || got.DarkMode != "dark" ||
		got.RefreshInterval != 120 {
		t.Fatalf("status %d settings %+v", response.Code, got)
	}

	_, err = database.ExecContext(t.Context(), "DROP TABLE settings")
	if err != nil {
		t.Fatal(err)
	}

	response = httptest.NewRecorder()
	handler.getSettings(
		response,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody),
	)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status %d body %s", response.Code, response.Body)
	}
}
