package services

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
)

func TestRestoreRejectsUnsupportedStructuresAtomically(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"changing view": `ALTER TABLE holdings RENAME TO original_holdings;
CREATE VIEW holdings AS SELECT id, name, ticker, asset_type,
CASE WHEN changes() = 3 THEN -100 ELSE 1 END AS quantity, price_provider, instrument_id,
base_currency, basket, location, notes, est_price, manual_price, manual_price_at, created_at, updated_at
FROM original_holdings`,
		"case alias cannot conceal invalid value": `UPDATE snapshots
SET holdings_json = '[{"id":1,"value_try":-1,"VALUE_TRY":1}]'`,
		"extra view":    `CREATE VIEW unexpected AS SELECT * FROM holdings`,
		"virtual table": `CREATE VIRTUAL TABLE unexpected USING fts5(body)`,
		"source trigger": `CREATE TRIGGER unexpected AFTER INSERT ON holdings
BEGIN UPDATE holdings SET quantity = -1 WHERE id = NEW.id; END`,
		"generated column": `ALTER TABLE holdings ADD COLUMN computed REAL
GENERATED ALWAYS AS (quantity * -1) VIRTUAL`,
		"missing table":  `DROP TABLE price_cache`,
		"missing column": `ALTER TABLE holdings RENAME COLUMN quantity TO amount`,
		"extra column":   `ALTER TABLE holdings ADD COLUMN unexpected TEXT`,
		"missing constraints": `ALTER TABLE settings RENAME TO old_settings;
CREATE TABLE settings AS SELECT * FROM old_settings`,
		"different affinity": `ALTER TABLE settings RENAME TO old_settings;
CREATE TABLE settings (key TEXT PRIMARY KEY, value BLOB NOT NULL,
updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
INSERT INTO settings SELECT * FROM old_settings`,
		"without rowid": `ALTER TABLE settings RENAME TO old_settings;
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL,
updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP) WITHOUT ROWID;
INSERT INTO settings SELECT * FROM old_settings`,
	}
	for name, statement := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := openTestDB(t, "source.db")
			seedRestoreValidationData(t, source, "Backup")
			execRestoreFixture(t, source, statement)
			assertBackupRejectedAtomically(t, source)
		})
	}
}

func TestRestoreRejectsExhaustedOrInvalidIdentities(t *testing.T) {
	t.Parallel()

	for _, table := range []string{"holdings", "snapshots", "price_cache"} {
		for _, value := range []string{"NULL", "'bad'", "1.5", "-1", "9223372036854775807", "1e999"} {
			t.Run(table+"/sequence/"+value, func(t *testing.T) {
				t.Parallel()
				source := openTestDB(t, "source.db")
				seedRestoreValidationData(t, source, "Backup")
				execRestoreFixture(
					t,
					source,
					"UPDATE sqlite_sequence SET seq = "+value+" WHERE name = '"+table+"'",
				)
				assertBackupRejectedAtomically(t, source)
			})
		}

		t.Run(table+"/duplicate sequence", func(t *testing.T) {
			t.Parallel()
			source := openTestDB(t, "source.db")
			seedRestoreValidationData(t, source, "Backup")
			execRestoreFixture(
				t,
				source,
				"INSERT INTO sqlite_sequence (name, seq) VALUES ('"+table+"', 10)",
			)
			assertBackupRejectedAtomically(t, source)
		})

		for _, value := range []string{"0", "-1", "9223372036854775807"} {
			t.Run(table+"/row/"+value, func(t *testing.T) {
				t.Parallel()
				source := openTestDB(t, "source.db")
				seedRestoreValidationData(t, source, "Backup")
				execRestoreFixture(t, source, "UPDATE "+table+" SET id = "+value+
					"; UPDATE sqlite_sequence SET seq = 0 WHERE name = '"+table+"'")
				assertBackupRejectedAtomically(t, source)
			})
		}
	}

	for _, identity := range []string{
		"", `"id":null,`, `"id":0,`, `"id":-1,`,
		`"id":9223372036854775807,`, `"id":9223372036854775808,`, `"id":1.5,`,
	} {
		t.Run("historical/"+identity, func(t *testing.T) {
			t.Parallel()
			source := openTestDB(t, "source.db")
			seedRestoreValidationData(t, source, "Backup")
			execRestoreFixture(
				t,
				source,
				`UPDATE snapshots SET holdings_json = '[{`+identity+`"quantity":1}]'`,
			)
			assertBackupRejectedAtomically(t, source)
		})
	}
}

func assertBackupRejectedAtomically(t *testing.T, source *SettingsService) {
	t.Helper()
	target := openTestDB(t, "target.db")
	seedRestoreValidationData(t, target, "Live")
	path := filepath.Join(t.TempDir(), "backup.db")

	err := source.Backup(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	err = target.RestoreFrom(t.Context(), path)
	if !errors.Is(err, ErrIncompatibleBackup) {
		t.Fatalf("RestoreFrom error = %v; want ErrIncompatibleBackup", err)
	}

	assertRestoreUnchanged(t, target)
}

func execRestoreFixture(t *testing.T, service *SettingsService, statement string) {
	t.Helper()

	_, err := service.db.ExecContext(t.Context(), statement)
	if err != nil {
		t.Fatal(err)
	}
}

func assertRestoreUnchanged(t *testing.T, target *SettingsService) {
	t.Helper()
	assertRestoreLiveData(t, target)

	if got := countRows(t, target, "SELECT generation FROM snapshot_state WHERE id = 1"); got != 0 {
		t.Errorf("generation = %d after rejected restore", got)
	}

	if got := countRows(t, target, `SELECT COUNT(*) FROM sqlite_sequence
WHERE name IN ('holdings', 'snapshots', 'price_cache') AND seq = 1`); got != 3 {
		t.Errorf("expected 3 unchanged sequences, got %d", got)
	}
}

func TestRestoreRejectsLiveTriggerWithoutExecutingIt(t *testing.T) {
	t.Parallel()
	source := openTestDB(t, "source.db")
	target := openTestDB(t, "target.db")
	seedRestoreValidationData(t, source, "Backup")
	seedRestoreValidationData(t, target, "Live")
	execRestoreFixture(t, target, `CREATE TRIGGER unexpected AFTER INSERT ON holdings
BEGIN UPDATE holdings SET quantity = -1 WHERE id = NEW.id; END`)
	path := filepath.Join(t.TempDir(), "backup.db")

	err := source.Backup(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	err = target.RestoreFrom(t.Context(), path)
	if !errors.Is(err, ErrIncompatibleBackup) {
		t.Fatalf("RestoreFrom error = %v; want ErrIncompatibleBackup", err)
	}

	assertRestoreUnchanged(t, target)
}

func TestRestoreRollsBackLateCopyFailure(t *testing.T) {
	t.Parallel()
	source := openTestDB(t, "source.db")
	target := openTestDB(t, "target.db")
	seedRestoreValidationData(t, source, "Backup")
	seedRestoreValidationData(t, target, "Live")
	execRestoreFixture(
		t,
		source,
		`INSERT INTO holdings (name, asset_type) VALUES ('Backup', 'Cash')`,
	)
	execRestoreFixture(t, target, `CREATE UNIQUE INDEX unique_holding_name ON holdings(name)`)
	path := filepath.Join(t.TempDir(), "backup.db")

	err := source.Backup(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	err = target.RestoreFrom(t.Context(), path)
	if !errors.Is(err, ErrIncompatibleBackup) {
		t.Fatalf("RestoreFrom error = %v; want ErrIncompatibleBackup", err)
	}

	assertRestoreUnchanged(t, target)
}

func TestRestoreCancellationLeavesLiveDataUnchanged(t *testing.T) {
	t.Parallel()
	source := openTestDB(t, "source.db")
	target := openTestDB(t, "target.db")
	seedRestoreValidationData(t, target, "Live")
	path := filepath.Join(t.TempDir(), "backup.db")

	err := source.Backup(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err = target.RestoreFrom(ctx, path)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RestoreFrom error = %v; want context.Canceled", err)
	}

	assertRestoreUnchanged(t, target)
}

func TestRestoreSnapshotValidationObservesCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := validateRestoreSnapshotJSON(ctx, `[{"id":1,"quantity":1}]`)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("validation error = %v; want context.Canceled", err)
	}
}

func TestRestoreValidationStopsBetweenRows(t *testing.T) {
	t.Parallel()
	service := openTestDB(t, "target.db")

	conn, err := service.db.Connx(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	_, err = conn.ExecContext(t.Context(), `ATTACH DATABASE ':memory:' AS restore_src;
CREATE TABLE restore_src.holdings (id INTEGER); INSERT INTO restore_src.holdings VALUES (1), (2)`)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	count := 0

	err = validateRestoreRows(ctx, conn, "id", "holdings", func(_ *sqlx.Rows) error {
		count++

		cancel()

		return nil
	})
	if !errors.Is(err, context.Canceled) || count != 1 {
		t.Fatalf("validated %d rows, error = %v; want 1 and context.Canceled", count, err)
	}
}
