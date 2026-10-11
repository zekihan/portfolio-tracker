package services

import (
	"path/filepath"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestSettingsReadReportsDatabaseFailure(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	err = database.Close()
	if err != nil {
		t.Fatal(err)
	}

	service := NewSettingsService(database)

	_, err = service.GetAll(t.Context())
	if err == nil {
		t.Fatal("settings read succeeded despite a closed database")
	}
}

func TestSettingsReadReportsInvalidValues(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"display_currency", "dark_mode", "refresh_interval"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			// Shadow the real table to simulate an invalid legacy NULL value.
			_, err = database.ExecContext(t.Context(),
				"CREATE TEMP TABLE settings (key TEXT, value TEXT)")
			if err != nil {
				t.Fatal(err)
			}

			_, err = database.ExecContext(t.Context(),
				"INSERT INTO settings (key, value) VALUES (?, NULL)", key)
			if err != nil {
				t.Fatal(err)
			}

			service := NewSettingsService(database)

			_, err = service.GetAll(t.Context())
			if err == nil {
				t.Fatal("settings read swallowed an invalid database value")
			}
		})
	}
}

func TestSettingsReadDefaultsForMissingKeys(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.ExecContext(t.Context(), "DELETE FROM settings")
	if err != nil {
		t.Fatal(err)
	}

	service := NewSettingsService(database)

	settings, err := service.GetAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if settings.DisplayCurrency != "TRY" || settings.DarkMode != "system" ||
		settings.RefreshInterval != 60 {
		t.Fatalf("unexpected defaults: %+v", settings)
	}
}
