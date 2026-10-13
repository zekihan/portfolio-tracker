package db

import (
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
)

func TestOpenMigratesSnapshotStateWithoutChangingUserData(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "v9.db")

	legacy, err := sqlx.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = legacy.Close() })

	for _, migration := range Migrations[:9] {
		_, err = legacy.ExecContext(t.Context(), migration)
		if err != nil {
			t.Fatal(err)
		}
	}

	_, err = legacy.ExecContext(t.Context(), `PRAGMA user_version = 9;
		INSERT INTO holdings (name, asset_type, quantity) VALUES ('Existing', 'Cash', 42);
		INSERT INTO snapshots (total_value_try, total_value_usd, usd_try_rate, holdings_json)
		VALUES (42, 1, 42, '[]');`)
	if err != nil {
		t.Fatal(err)
	}

	err = legacy.Close()
	if err != nil {
		t.Fatal(err)
	}

	migrated, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = migrated.Close() })

	var generation, holdings, snapshots int

	err = migrated.GetContext(
		t.Context(),
		&generation,
		"SELECT generation FROM snapshot_state WHERE id = 1",
	)
	if err != nil {
		t.Fatal(err)
	}

	err = migrated.GetContext(
		t.Context(),
		&holdings,
		"SELECT COUNT(*) FROM holdings WHERE quantity = 42",
	)
	if err != nil {
		t.Fatal(err)
	}

	err = migrated.GetContext(
		t.Context(),
		&snapshots,
		"SELECT COUNT(*) FROM snapshots WHERE total_value_try = 42",
	)
	if err != nil {
		t.Fatal(err)
	}

	if generation != 0 || holdings != 1 || snapshots != 1 {
		t.Fatalf(
			"migration changed data: generation=%d holdings=%d snapshots=%d",
			generation,
			holdings,
			snapshots,
		)
	}
}
