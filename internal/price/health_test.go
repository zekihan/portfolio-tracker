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

	latest := time.Now().UTC().Truncate(time.Second)

	_, err = database.ExecContext(t.Context(), `
		INSERT INTO price_cache
		(provider, instrument_id, price, currency, source, fetched_at, expires_at,
		error_count, last_error)
		VALUES ('Frankfurter', 'USD', 40, 'TRY', 'test', ?, ?, 0, ''),
		('Frankfurter', 'EUR', 45, 'TRY', 'test', ?, ?, 0, ''),
		('Frankfurter', 'GBP', 50, 'TRY', 'test', ?, ?, 1, 'failed')
	`, latest.Add(-time.Hour), latest.Add(time.Hour),
		latest, latest.Add(time.Hour), latest.Add(time.Minute), latest.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	service := NewService(database, DefaultRegistry())

	health, err := service.GetProviderHealth()
	if err != nil {
		t.Fatal(err)
	}

	found := false

	for _, provider := range health {
		if provider.Name == "Frankfurter" {
			found = true

			if !provider.LastOK.Equal(latest) {
				t.Errorf("last successful fetch = %v, want %v", provider.LastOK, latest)
			}
		} else if !provider.LastOK.IsZero() {
			t.Errorf("provider %s with no cache has last fetch %v", provider.Name, provider.LastOK)
		}
	}

	if !found {
		t.Fatal("Frankfurter missing from provider health")
	}
}
