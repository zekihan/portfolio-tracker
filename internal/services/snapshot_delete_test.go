package services

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestDeleteSnapshotRemovesOnlySelectedSnapshot(t *testing.T) {
	t.Parallel()

	database := openSnapshotDatabase(t, filepath.Join(t.TempDir(), "portfolio.db"))

	_, err := database.ExecContext(t.Context(), `INSERT INTO snapshots
  (id, total_value_try, total_value_usd, usd_try_rate, holdings_json)
  VALUES (1, 10, 1, 10, '[]'), (2, 20, 2, 10, '[]')`)
	if err != nil {
		t.Fatal(err)
	}

	service := NewSnapshotService(database, nil, nil)

	err = service.DeleteSnapshot(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}

	if got := countRows(
		t,
		NewSettingsService(database),
		"SELECT COUNT(*) FROM snapshots WHERE id = 2",
	); got != 1 {
		t.Fatal("unselected snapshot deleted")
	}

	if got := countRows(
		t,
		NewSettingsService(database),
		"SELECT generation FROM snapshot_state WHERE id = 1",
	); got != 0 {
		t.Fatal("individual deletion changed generation")
	}

	err = service.DeleteSnapshot(t.Context(), 1)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing snapshot error = %v", err)
	}
}

func TestClearHistoryPreservesCurrentData(t *testing.T) {
	t.Parallel()

	database := openSnapshotDatabase(t, filepath.Join(t.TempDir(), "portfolio.db"))

	_, err := database.ExecContext(t.Context(), `
  INSERT INTO holdings (name, asset_type) VALUES ('Current', 'Stock');
  INSERT INTO snapshots (total_value_try, total_value_usd, usd_try_rate, holdings_json)
   VALUES (1, 1, 1, '[]'), (2, 2, 1, '[]');
  INSERT INTO price_cache (provider, instrument_id, price, currency, source, expires_at)
   VALUES ('Yahoo Finance', 'AAPL', 1, 'USD', 'test', CURRENT_TIMESTAMP);
  UPDATE settings SET value = 'dark' WHERE key = 'dark_mode';
  UPDATE provider_registry SET enabled = 0 WHERE name = 'Yahoo Finance';
  INSERT INTO snapshot_runs (slot, owner, acquired_at, completed) VALUES (1, 'done', 0, 1);`)
	if err != nil {
		t.Fatal(err)
	}

	service := NewSnapshotService(database, nil, nil)
	for generation := 1; generation <= 2; generation++ {
		err = service.ClearHistory(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		checks := map[string]int{
			"SELECT COUNT(*) FROM snapshots":                                                      0,
			"SELECT COUNT(*) FROM holdings WHERE name = 'Current'":                                1,
			"SELECT COUNT(*) FROM price_cache WHERE instrument_id = 'AAPL'":                       1,
			"SELECT COUNT(*) FROM settings WHERE key = 'dark_mode' AND value = 'dark'":            1,
			"SELECT COUNT(*) FROM provider_registry WHERE name = 'Yahoo Finance' AND enabled = 0": 1,
			"SELECT COUNT(*) FROM snapshot_runs WHERE slot = 1 AND completed = 1":                 1,
			"SELECT generation FROM snapshot_state WHERE id = 1":                                  generation,
		}

		for query, want := range checks {
			if got := countRows(t, NewSettingsService(database), query); got != want {
				t.Errorf("%s = %d, want %d", query, got, want)
			}
		}
	}
}

func TestClearHistoryRollsBackDeletionAndGeneration(t *testing.T) {
	t.Parallel()

	database := openSnapshotDatabase(t, filepath.Join(t.TempDir(), "portfolio.db"))

	_, err := database.ExecContext(t.Context(), `
  INSERT INTO snapshots (total_value_try, total_value_usd, usd_try_rate, holdings_json)
   VALUES (1, 1, 1, '[]'), (2, 2, 1, '[]');
  CREATE TRIGGER reject_history_delete BEFORE DELETE ON snapshots WHEN OLD.id = 2
   BEGIN SELECT RAISE(ABORT, 'injected deletion failure'); END;`)
	if err != nil {
		t.Fatal(err)
	}

	err = NewSnapshotService(database, nil, nil).ClearHistory(t.Context())
	if err == nil {
		t.Fatal("clear succeeded despite deletion failure")
	}

	if got := countRows(
		t,
		NewSettingsService(database),
		"SELECT COUNT(*) FROM snapshots",
	); got != 2 {
		t.Errorf("snapshot count = %d, want 2", got)
	}

	if got := countRows(
		t,
		NewSettingsService(database),
		"SELECT generation FROM snapshot_state WHERE id = 1",
	); got != 0 {
		t.Errorf("generation = %d, want 0", got)
	}
}
