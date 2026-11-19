package price

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestProviderHealthLastSuccessfulFetch(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	const providerName = "Garanti BBVA"

	latest := time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC)

	_, err = database.ExecContext(t.Context(), `
		INSERT INTO price_cache
		(provider, instrument_id, price, currency, source, fetched_at, expires_at,
		error_count, last_error)
		VALUES (?, 'OLDER', 40, 'TRY', 'test', ?, ?, 0, ''),
		(?, 'LATEST', 45, 'TRY', 'test', ?, ?, 0, '')
	`, providerName, latest.Add(-time.Hour), latest.Add(time.Hour),
		providerName, latest, latest.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	service := NewService(database, NewRegistry())

	service.recordFailureContext(t.Context(), providerName, "LATEST", errProviderUnavailable)

	cached, err := service.getCachedPriceContext(t.Context(), providerName, "LATEST")
	if err != nil || cached == nil || cached.ErrorCount != 1 {
		t.Fatalf("quote after a failed refresh = %+v, %v; want one recorded failure", cached, err)
	}

	health, err := service.GetProviderHealth()
	if err != nil {
		t.Fatal(err)
	}

	found := false

	for _, provider := range health {
		if provider.Name == providerName {
			found = true

			if !provider.LastOK.Equal(latest) {
				t.Errorf("last successful fetch = %v, want %v", provider.LastOK, latest)
			}
		} else if !provider.LastOK.IsZero() {
			t.Errorf("provider %s with no cache has last fetch %v", provider.Name, provider.LastOK)
		}
	}

	if !found {
		t.Fatalf("%s missing from provider health", providerName)
	}
}
