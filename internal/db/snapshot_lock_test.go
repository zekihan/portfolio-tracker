package db

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

func TestOpenMigratesSnapshotLockOwnership(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "legacy.db")

	legacy, err := sqlx.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = legacy.Close() })

	for _, migration := range Migrations[:7] {
		_, err = legacy.ExecContext(t.Context(), migration)
		if err != nil {
			t.Fatal(err)
		}
	}

	_, err = legacy.ExecContext(t.Context(), "PRAGMA user_version = 7")
	if err != nil {
		t.Fatal(err)
	}

	heldAt := time.Now().UTC().Truncate(time.Second)

	_, err = legacy.ExecContext(t.Context(),
		"INSERT INTO snapshot_lock (id, acquired_at) VALUES (1, ?)",
		heldAt,
	)
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

	var lock struct {
		AcquiredAt time.Time `db:"acquired_at"`
		Owner      string    `db:"owner"`
	}

	err = migrated.Get(
		&lock,
		"SELECT acquired_at, owner FROM snapshot_lock WHERE id = 1",
	)
	if err != nil {
		t.Fatal(err)
	}

	if lock.Owner != "" || !lock.AcquiredAt.Equal(heldAt) {
		t.Fatalf("legacy lock changed: %+v", lock)
	}
}
