package db

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jmoiron/sqlx"
)

func TestOpenMigratesHoldingReceiptsWithoutChangingUserData(t *testing.T) {
	t.Parallel()

	for _, version := range []int{9, 10} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "legacy.db")

			legacy, err := sqlx.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = legacy.Close() })

			for _, migration := range Migrations[:version] {
				_, err = legacy.ExecContext(t.Context(), migration)
				if err != nil {
					t.Fatal(err)
				}
			}

			_, err = legacy.ExecContext(t.Context(), "PRAGMA user_version = "+strconv.Itoa(version)+
				`; INSERT INTO holdings (name, asset_type, quantity) VALUES ('Existing', 'Cash', 42)`)
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

			var holdings, receipts int

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
				&receipts,
				"SELECT COUNT(*) FROM holding_create_receipts",
			)
			if err != nil {
				t.Fatal(err)
			}

			if holdings != 1 || receipts != 0 {
				t.Fatalf("migration changed user data: holdings=%d receipts=%d", holdings, receipts)
			}
		})
	}
}
