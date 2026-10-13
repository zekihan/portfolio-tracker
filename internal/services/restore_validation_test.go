package services

import (
	"errors"
	"path/filepath"
	"strconv"
	"testing"
)

func TestRestoreRejectsInvalidDataAtomically(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"quantity text":              `UPDATE holdings SET quantity = 'not-a-number'`,
		"negative quantity":          `UPDATE holdings SET quantity = -1`,
		"infinite quantity":          `UPDATE holdings SET quantity = 1e999`,
		"invalid estimate":           `UPDATE holdings SET est_price = 'oops'`,
		"negative manual price":      `UPDATE holdings SET manual_price = -1`,
		"invalid holding date":       `UPDATE holdings SET updated_at = 'yesterday'`,
		"invalid cache price":        `UPDATE price_cache SET price = 'oops'`,
		"infinite cache price":       `UPDATE price_cache SET price = 1e999`,
		"invalid cache count":        `UPDATE price_cache SET error_count = 1.5`,
		"invalid cache date":         `UPDATE price_cache SET expires_at = 'never'`,
		"invalid snapshot total":     `UPDATE snapshots SET total_value_try = 'oops'`,
		"unpaired EUR total":         `UPDATE snapshots SET total_value_eur = 1`,
		"unpaired EUR rate":          `UPDATE snapshots SET eur_try_rate = 40`,
		"invalid EUR total":          `UPDATE snapshots SET total_value_eur = 'oops', eur_try_rate = 40`,
		"negative EUR total":         `UPDATE snapshots SET total_value_eur = -1, eur_try_rate = 40`,
		"infinite EUR total":         `UPDATE snapshots SET total_value_eur = 1e999, eur_try_rate = 40`,
		"invalid EUR rate":           `UPDATE snapshots SET total_value_eur = 1, eur_try_rate = 'oops'`,
		"zero EUR rate":              `UPDATE snapshots SET total_value_eur = 1, eur_try_rate = 0`,
		"negative EUR rate":          `UPDATE snapshots SET total_value_eur = 1, eur_try_rate = -1`,
		"infinite EUR rate":          `UPDATE snapshots SET total_value_eur = 1, eur_try_rate = 1e999`,
		"infinite snapshot rate":     `UPDATE snapshots SET usd_try_rate = 1e999`,
		"zero snapshot rate":         `UPDATE snapshots SET usd_try_rate = 0`,
		"negative snapshot count":    `UPDATE snapshots SET failed_count = -1`,
		"invalid snapshot json":      `UPDATE snapshots SET holdings_json = '{'`,
		"invalid snapshot quantity":  `UPDATE snapshots SET holdings_json = '[{"id":1,"quantity":"oops"}]'`,
		"negative snapshot quantity": `UPDATE snapshots SET holdings_json = '[{"id":1,"quantity":-1}]'`,
		"invalid JSON EUR value":     `UPDATE snapshots SET holdings_json = '[{"id":1,"value_eur":"oops"}]'`,
		"negative JSON EUR value":    `UPDATE snapshots SET holdings_json = '[{"id":1,"value_eur":-1}]'`,
		"infinite JSON EUR value":    `UPDATE snapshots SET holdings_json = '[{"id":1,"value_eur":1e999}]'`,
		"EUR case alias":             `UPDATE snapshots SET holdings_json = '[{"id":1,"value_eur":-1,"VALUE_EUR":1}]'`,
		"trailing snapshot json":     `UPDATE snapshots SET holdings_json = '[] []'`,
		"invalid settings interval":  `UPDATE settings SET value = 'fast' WHERE key = 'refresh_interval'`,
		"invalid settings currency":  `UPDATE settings SET value = 'XYZ' WHERE key = 'display_currency'`,
		"invalid settings mode":      `UPDATE settings SET value = 'unknown' WHERE key = 'dark_mode'`,
		"invalid settings date":      `UPDATE settings SET updated_at = 'today'`,
	}
	for name, mutation := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := openTestDB(t, "source.db")
			target := openTestDB(t, "target.db")
			seedRestoreValidationData(t, source, "Backup")
			seedRestoreValidationData(t, target, "Live")

			_, err := source.db.ExecContext(t.Context(), mutation)
			if err != nil {
				t.Fatal(err)
			}

			path := filepath.Join(t.TempDir(), "backup.db")

			err = source.Backup(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}

			err = target.RestoreFrom(t.Context(), path)
			if !errors.Is(err, ErrIncompatibleBackup) {
				t.Fatalf("RestoreFrom error = %v; want ErrIncompatibleBackup", err)
			}

			assertRestoreLiveData(t, target)
		})
	}
}

func seedRestoreValidationData(t *testing.T, service *SettingsService, name string) {
	t.Helper()

	_, err := service.db.ExecContext(t.Context(), `INSERT INTO holdings
(name, asset_type, quantity, ticker, price_provider, instrument_id, basket, location, notes, est_price)
VALUES (?, 'Stock', 2, 'TEST', 'Manual', 'TEST', '', '', '', 0)`, name)
	if err != nil {
		t.Fatal(err)
	}

	statements := []string{
		`INSERT INTO snapshots (total_value_try, total_value_usd, usd_try_rate, holdings_json)
VALUES (2, 1, 2, '[{"id":1,"quantity":2,"price":1,"value_try":2,"value_usd":1}]')`,
		`INSERT INTO price_cache (provider, instrument_id, price, currency, source, expires_at, last_error)
VALUES ('Manual', 'TEST', 1, 'USD', 'manual', CURRENT_TIMESTAMP, '')`,
	}
	for _, statement := range statements {
		_, err = service.db.ExecContext(t.Context(), statement)
		if err != nil {
			t.Fatal(err)
		}
	}

	_, err = service.db.ExecContext(
		t.Context(),
		`INSERT INTO settings (key, value) VALUES ('marker', ?)`,
		name,
	)
	if err != nil {
		t.Fatal(err)
	}
}

func assertRestoreLiveData(t *testing.T, service *SettingsService) {
	t.Helper()

	checks := []string{
		`SELECT COUNT(*) FROM holdings WHERE name = 'Live' AND quantity = 2`,
		`SELECT COUNT(*) FROM snapshots WHERE total_value_try = 2 AND usd_try_rate = 2`,
		`SELECT COUNT(*) FROM price_cache WHERE price = 1 AND error_count = 0`,
		`SELECT COUNT(*) FROM settings WHERE key = 'marker' AND value = 'Live'`,
	}
	for _, query := range checks {
		if got := countRows(t, service, query); got != 1 {
			t.Errorf("live data changed: %s = %d", query, got)
		}
	}

	_, err := NewHoldingService(service.db, nil).GetAll(t.Context())
	if err != nil {
		t.Fatalf("live holdings unreadable: %v", err)
	}
}

func TestRestoreValidLegacyData(t *testing.T) {
	t.Parallel()
	source := openTestDB(t, "source.db")
	target := openTestDB(t, "target.db")
	seedRestoreValidationData(t, source, "Legacy")
	useLegacyRestoreSchema(t, source, 9)

	statements := []string{
		`DROP TABLE snapshot_runs`,
		`DROP TABLE snapshot_state`,
		`UPDATE holdings SET manual_price = NULL, manual_price_at = NULL`,
		`UPDATE snapshots SET holdings_json = 'null'`,
		`UPDATE settings SET value = '' WHERE key = 'dark_mode'`,
		`INSERT INTO settings (key, value) VALUES ('unknown_legacy_key', 'preserved')`,
	}
	for _, statement := range statements {
		_, err := source.db.ExecContext(t.Context(), statement)
		if err != nil {
			t.Fatal(err)
		}
	}

	path := filepath.Join(t.TempDir(), "legacy.db")

	err := source.Backup(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	err = target.RestoreFrom(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	holdings, err := NewHoldingService(target.db, nil).GetAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if len(holdings) != 1 || holdings[0].Name != "Legacy" {
		t.Fatalf("restored holdings = %#v", holdings)
	}

	if got := countRows(
		t,
		target,
		`SELECT COUNT(*) FROM settings WHERE key = 'unknown_legacy_key' AND value = 'preserved'`,
	); got != 1 {
		t.Fatal("legacy setting was not preserved")
	}
}

// Match legacy user-table columns rather than merely relabeling a v12 backup.
func useLegacyRestoreSchema(t *testing.T, service *SettingsService, version int) {
	t.Helper()
	execRestoreFixture(t, service, `ALTER TABLE snapshots DROP COLUMN total_value_eur;
ALTER TABLE snapshots DROP COLUMN eur_try_rate;
PRAGMA user_version = `+strconv.Itoa(version))
}
