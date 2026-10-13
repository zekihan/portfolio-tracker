package services

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestRestoreLegacySchemasKeepEURUnavailable(t *testing.T) {
	t.Parallel()

	for _, version := range []int{9, 10, 11} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			t.Parallel()
			source := openLegacyEURRestoreDB(t, version)
			seedRestoreValidationData(t, source, "Legacy")
			execRestoreFixture(t, source, `INSERT INTO holdings (id, name, asset_type)
VALUES (50, 'Deleted', 'Cash'); DELETE FROM holdings WHERE id = 50`)
			target := openTestDB(t, "target.db")
			seedRestoreValidationData(t, target, "Live")
			execRestoreFixture(t, target,
				"UPDATE snapshots SET total_value_eur = 1, eur_try_rate = 2")
			path := filepath.Join(t.TempDir(), "legacy.db")

			err := source.Backup(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}

			err = target.RestoreFrom(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}

			if got := countRows(t, target, `SELECT COUNT(*) FROM snapshots
WHERE total_value_eur IS NULL AND eur_try_rate IS NULL AND total_value_try = 2
AND total_value_usd = 1 AND usd_try_rate = 2`); got != 1 {
				t.Fatal("legacy snapshot valuation changed or received invented EUR history")
			}

			assertRestoredEURHistory(t, target, "null")

			if got := countRows(
				t,
				target,
				`SELECT seq FROM sqlite_sequence WHERE name = 'holdings'`,
			); got != 50 {
				t.Fatalf("restored holding high-water mark = %d; want 50", got)
			}

			backup, err := sqlx.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = backup.Close() })

			var actualVersion, addedColumns int

			err = backup.GetContext(t.Context(), &actualVersion, "PRAGMA user_version")
			if err != nil || actualVersion != version {
				t.Fatalf("backup version changed: %d, %v", actualVersion, err)
			}

			err = backup.GetContext(t.Context(), &addedColumns, `SELECT COUNT(*)
FROM pragma_table_info('snapshots') WHERE name IN ('total_value_eur', 'eur_try_rate')`)
			if err != nil || addedColumns != 0 {
				t.Fatalf("backup was migrated in place: %d EUR columns, %v", addedColumns, err)
			}
		})
	}
}

func TestRestoreEURValuesAndSettings(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"0", "2.5", "null"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			source := openTestDB(t, "source.db")
			target := openTestDB(t, "target.db")
			seedRestoreValidationData(t, source, "Euro holding")
			execRestoreFixture(t, source, `UPDATE holdings SET base_currency = 'EUR';
UPDATE settings SET value = 'EUR' WHERE key = 'display_currency';
UPDATE snapshots SET total_value_eur = 0, eur_try_rate = 40,
holdings_json = '[{"id":1,"value_eur":`+value+`}]'`)
			path := filepath.Join(t.TempDir(), "eur.db")

			err := source.Backup(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}

			err = target.RestoreFrom(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}

			for _, query := range []string{
				`SELECT COUNT(*) FROM holdings WHERE base_currency = 'EUR'`,
				`SELECT COUNT(*) FROM settings WHERE key = 'display_currency' AND value = 'EUR'`,
				`SELECT COUNT(*) FROM snapshots WHERE total_value_eur = 0 AND eur_try_rate = 40`,
				`SELECT COUNT(*) FROM snapshots WHERE holdings_json = '[{"id":1,"value_eur":` + value + `}]'`,
			} {
				if got := countRows(t, target, query); got != 1 {
					t.Fatalf("EUR value not preserved: %s", query)
				}
			}

			assertRestoredEURHistory(t, target, value)
		})
	}
}

func assertRestoredEURHistory(t *testing.T, target *SettingsService, value string) {
	t.Helper()

	history, err := NewSnapshotService(target.db, nil, nil).GetHoldingHistory(t.Context(), 1, 1)
	if err != nil || len(history) != 1 {
		t.Fatalf("restored holding history = %+v, %v", history, err)
	}

	if value == "null" {
		if history[0].ValueEUR != nil {
			t.Fatalf("unavailable EUR holding value became %v", *history[0].ValueEUR)
		}

		return
	}

	want, err := strconv.ParseFloat(value, 64)
	if err != nil || history[0].ValueEUR == nil || *history[0].ValueEUR != want {
		t.Fatalf("restored EUR holding value = %+v, %v; want %s", history[0], err, value)
	}
}

func TestRestoreRejectsEURSchemaVersionMismatches(t *testing.T) {
	t.Parallel()

	for name, mutation := range map[string]string{
		"old version with EUR columns":      "PRAGMA user_version = 11",
		"current version missing EUR total": "ALTER TABLE snapshots DROP COLUMN total_value_eur",
		"current version missing EUR rate":  "ALTER TABLE snapshots DROP COLUMN eur_try_rate",
		"unknown old version":               "PRAGMA user_version = 8",
		"unknown future version":            "PRAGMA user_version = 13",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := openTestDB(t, "source.db")
			seedRestoreValidationData(t, source, "Backup")
			execRestoreFixture(t, source, mutation)
			assertBackupRejectedAtomically(t, source)
		})
	}
}

func openLegacyEURRestoreDB(t *testing.T, version int) *SettingsService {
	t.Helper()

	database, err := sqlx.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })
	database.SetMaxOpenConns(1)

	for _, migration := range db.Migrations[:version] {
		_, err = database.ExecContext(t.Context(), migration)
		if err != nil {
			t.Fatal(err)
		}
	}

	_, err = database.ExecContext(t.Context(), "PRAGMA user_version = "+strconv.Itoa(version))
	if err != nil {
		t.Fatal(err)
	}

	return NewSettingsService(database)
}
