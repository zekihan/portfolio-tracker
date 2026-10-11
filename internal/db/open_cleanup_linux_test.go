package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
)

func countDatabaseDescriptors(t *testing.T, path string) int {
	t.Helper()

	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}

	count := 0

	for _, entry := range entries {
		target, readErr := os.Readlink("/proc/self/fd/" + entry.Name())
		if readErr == nil && target == path {
			count++
		}
	}

	return count
}

func TestOpenClosesDatabaseOnMigrationFailure(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "unsupported.db")

	database, err := sqlx.Connect("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}

	_, err = database.ExecContext(t.Context(), "PRAGMA user_version = 2147483647")
	closeErr := database.Close()

	if err != nil {
		t.Fatal(err)
	}

	if closeErr != nil {
		t.Fatal(closeErr)
	}

	before := countDatabaseDescriptors(t, path)
	for range 3 {
		opened, openErr := Open(path)
		if openErr == nil {
			_ = opened.Close()

			t.Fatal("accepted unsupported schema")
		}
	}

	after := countDatabaseDescriptors(t, path)
	if after != before {
		t.Errorf("database descriptors grew from %d to %d after failed opens", before, after)
	}
}
