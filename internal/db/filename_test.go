package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenPreservesMemoryConnectionModes(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"private", "temporary", "URI"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "memory.db")

			dsn := ":memory:"
			if mode == "temporary" {
				dsn = ""
			}

			if mode == "URI" {
				dsn = "file:" + path + "?mode=memory&cache=shared"
			}

			database, err := Open(dsn)
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			value, err := GetSettingContext(t.Context(), database, "display_currency")
			if err != nil || value != "TRY" {
				t.Fatalf("memory database initialization failed: %q, %v", value, err)
			}

			_, statErr := os.Stat(path)
			if !os.IsNotExist(statErr) {
				t.Errorf("memory database created a disk file: %v", statErr)
			}
		})
	}
}

func TestOpenPreservesLiteralDatabaseFilename(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"portfolio.db", "portfolio?notes.db", "portfolio#notes.db", "portfolio%20notes.db"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), name)

			database, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			_, statErr := os.Stat(path)
			if statErr != nil {
				t.Fatalf("requested database file not created: %v", statErr)
			}

			_, err = database.ExecContext(
				t.Context(),
				"UPDATE settings SET value = 'USD' WHERE key = 'display_currency'",
			)
			if err != nil {
				t.Fatal(err)
			}

			err = database.Close()
			if err != nil {
				t.Fatal(err)
			}

			reopened, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = reopened.Close() })

			value, err := GetSettingContext(t.Context(), reopened, "display_currency")
			if err != nil {
				t.Fatal(err)
			}

			if value != "USD" {
				t.Errorf("reopened setting = %q, want USD", value)
			}

			var foreignKeys int

			err = reopened.GetContext(t.Context(), &foreignKeys, "PRAGMA foreign_keys")
			if err != nil {
				t.Fatal(err)
			}

			if foreignKeys != 1 {
				t.Error("foreign keys not enabled")
			}
		})
	}
}
