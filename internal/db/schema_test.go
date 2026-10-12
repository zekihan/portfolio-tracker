package db

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jmoiron/sqlx"
)

func TestMigrateRejectsUnsupportedVersions(t *testing.T) {
	t.Parallel()

	for _, version := range []int{-1, len(Migrations) + 1} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			t.Parallel()

			database, err := sqlx.Open("sqlite", filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			_, err = database.ExecContext(
				t.Context(),
				"PRAGMA user_version = "+strconv.Itoa(version),
			)
			if err != nil {
				t.Fatal(err)
			}

			err = migrate(database)
			if err == nil {
				t.Error("migration accepted an unsupported schema version")
			}

			var actual int

			err = database.GetContext(t.Context(), &actual, "PRAGMA user_version")
			if err != nil {
				t.Fatal(err)
			}

			if actual != version {
				t.Errorf("schema version changed to %d, want %d", actual, version)
			}
		})
	}
}

func TestOpenMigratesFreshDatabase(t *testing.T) {
	t.Parallel()

	database, err := Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	var version int

	err = database.GetContext(t.Context(), &version, "PRAGMA user_version")
	if err != nil {
		t.Fatal(err)
	}

	if version != len(Migrations) {
		t.Errorf("schema version = %d, want %d", version, len(Migrations))
	}
}
