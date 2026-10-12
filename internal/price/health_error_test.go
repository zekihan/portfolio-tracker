package price

import (
	"path/filepath"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
)

func configureHealthReadFailure(t *testing.T, service *Service, scenario string) {
	t.Helper()

	var err error

	switch scenario {
	case "missing cache table":
		_, err = service.db.ExecContext(t.Context(), "DROP TABLE price_cache")
	case "nullable error metadata":
		_, err = service.db.ExecContext(t.Context(), `INSERT INTO price_cache
		(provider, instrument_id, price, currency, source, error_count, last_error, expires_at)
		VALUES ('Frankfurter', 'USD', 40, 'TRY', 'test', 1, NULL, '2000-01-01 00:00:00')`)
		service.failures["Frankfurter"] = 1
	default:
	}

	if err != nil {
		t.Fatal(err)
	}
}

func TestProviderHealthReportsCacheReadFailures(t *testing.T) {
	t.Parallel()

	for _, scenario := range []string{"empty cache", "missing cache table", "nullable error metadata"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			service := NewService(database, DefaultRegistry())

			configureHealthReadFailure(t, service, scenario)

			health, err := service.GetProviderHealth()
			if scenario == "missing cache table" {
				if err == nil {
					t.Error("suppressed cache read error")
				}

				if len(health) != 0 {
					t.Error("returned misleading partial health report")
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if len(health) == 0 {
				t.Fatal("missing providers in empty-cache health report")
			}

			checkLegacyHealth(t, health, scenario)
		})
	}
}

func checkLegacyHealth(t *testing.T, health []models.ProviderHealth, scenario string) {
	t.Helper()

	for _, provider := range health {
		if scenario == "nullable error metadata" && provider.Name == "Frankfurter" &&
			provider.ErrorMsg != "Provider request failed" {
			t.Errorf("nullable diagnostic = %q", provider.ErrorMsg)
		}

		if !provider.LastOK.IsZero() {
			t.Errorf("unexpected last fetch for %s", provider.Name)
		}
	}
}
