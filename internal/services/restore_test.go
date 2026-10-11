package services

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func openTestDB(t *testing.T, name string) *SettingsService {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	return NewSettingsService(database)
}

func countRows(t *testing.T, service *SettingsService, query string) int {
	t.Helper()

	var count int

	err := service.db.GetContext(t.Context(), &count, query)
	if err != nil {
		t.Fatal(err)
	}

	return count
}

func TestRestoreReplacesLiveData(t *testing.T) {
	t.Parallel()

	source := openTestDB(t, "source.db")
	target := openTestDB(t, "target.db")

	seed := func(service *SettingsService, statements ...string) {
		t.Helper()

		for _, statement := range statements {
			_, err := service.db.ExecContext(t.Context(), statement)
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	seed(
		source,
		`INSERT INTO holdings (name, asset_type) VALUES ('Backed up', 'Stock')`,
		`INSERT INTO snapshots (total_value_try, total_value_usd, usd_try_rate, holdings_json) VALUES (1, 1, 1, '[]')`,
		`UPDATE settings SET value = 'dark' WHERE key = 'dark_mode'`,
	)
	seed(target,
		`INSERT INTO holdings (name, asset_type) VALUES ('Live A', 'Stock'), ('Live B', 'Stock')`,
		`INSERT INTO price_cache (provider, instrument_id, price, currency, source, expires_at)
			VALUES ('Yahoo Finance', 'X', 1, 'USD', 'test', CURRENT_TIMESTAMP)`,
	)

	backupPath := filepath.Join(t.TempDir(), "backup.db")

	err := source.Backup(t.Context(), backupPath)
	if err != nil {
		t.Fatal(err)
	}

	err = target.RestoreFrom(t.Context(), backupPath)
	if err != nil {
		t.Fatal(err)
	}

	if got := countRows(t, target, "SELECT COUNT(*) FROM holdings"); got != 1 {
		t.Errorf("holdings = %d after restore, want 1", got)
	}

	if got := countRows(
		t,
		target,
		"SELECT COUNT(*) FROM holdings WHERE name = 'Backed up'",
	); got != 1 {
		t.Error("restored holding not found")
	}

	if got := countRows(t, target, "SELECT COUNT(*) FROM snapshots"); got != 1 {
		t.Errorf("snapshots = %d after restore, want 1", got)
	}

	if got := countRows(t, target, "SELECT COUNT(*) FROM price_cache"); got != 0 {
		t.Errorf("price_cache = %d after restore, want 0 (replaced by backup)", got)
	}

	settings, err := target.GetAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if settings.DarkMode != "dark" {
		t.Errorf("dark mode = %q after restore, want dark", settings.DarkMode)
	}
}

func TestRestoreRejectsIncompatibleBackupWithoutChangingData(t *testing.T) {
	t.Parallel()

	target := openTestDB(t, "target.db")

	_, err := target.db.ExecContext(
		t.Context(),
		`INSERT INTO holdings (name, asset_type) VALUES ('Live', 'Stock')`,
	)
	if err != nil {
		t.Fatal(err)
	}

	other := openTestDB(t, "other.db")

	_, err = other.db.ExecContext(t.Context(), `PRAGMA user_version = 99`)
	if err != nil {
		t.Fatal(err)
	}

	wrongVersion := filepath.Join(t.TempDir(), "wrong-version.db")

	err = other.Backup(t.Context(), wrongVersion)
	if err != nil {
		t.Fatal(err)
	}

	notSQLite := filepath.Join(t.TempDir(), "text.db")

	err = os.WriteFile(notSQLite, []byte("this is not a database"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{wrongVersion, notSQLite} {
		restoreErr := target.RestoreFrom(t.Context(), path)
		if restoreErr == nil {
			t.Fatalf("RestoreFrom(%s) error = nil, want error", filepath.Base(path))
		}

		if !errors.Is(restoreErr, ErrIncompatibleBackup) {
			t.Errorf(
				"RestoreFrom(%s) error = %v, want ErrIncompatibleBackup",
				filepath.Base(path),
				restoreErr,
			)
		}

		if got := countRows(
			t,
			target,
			"SELECT COUNT(*) FROM holdings WHERE name = 'Live'",
		); got != 1 {
			t.Errorf("live holding changed after rejected restore %s", filepath.Base(path))
		}
	}
}
