package db

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"

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

func TestMigrateWaitsForConcurrentStartup(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "shared.db")

	synctest.Test(t, func(t *testing.T) {
		writer, err := sqlx.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = writer.Close() }()

		conn, err := writer.Connx(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()

		_, err = conn.ExecContext(t.Context(),
			"BEGIN IMMEDIATE;\n"+strings.Join(Migrations, ";\n")+
				";\nPRAGMA user_version = "+strconv.Itoa(len(Migrations)))
		if err != nil {
			t.Fatal(err)
		}

		dsn, err := sqliteDSN(path)
		if err != nil {
			t.Fatal(err)
		}

		contender, err := sqlx.Open("sqlite", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = contender.Close() }()

		contender.SetMaxOpenConns(1)

		// Verify the older version is visible. The cancellable query also installs
		// the driver's Go busy handler so synctest can observe the lock wait.
		var version int

		err = contender.GetContext(t.Context(), &version, "PRAGMA user_version")
		if err != nil || version != 0 {
			t.Fatalf("uncommitted schema version = %d, err = %v; want 0", version, err)
		}

		migrated := make(chan error, 1)
		go func() { migrated <- migrate(contender) }()

		synctest.Wait()

		_, err = conn.ExecContext(t.Context(), "COMMIT")
		if err != nil {
			t.Fatal(err)
		}

		err = <-migrated
		if err != nil {
			t.Fatalf("migrate while another replica migrated: %v", err)
		}
	})
}
