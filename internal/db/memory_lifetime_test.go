package db

import (
	"testing"
)

func TestMemoryDatabaseFormsRetainData(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"", ":memory:", "file::memory:", "file:named-lifetime-test?mode=memory&cache=shared"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			database, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			_, err = database.ExecContext(
				t.Context(),
				"INSERT INTO holdings (name, asset_type) VALUES ('Retained', 'Cash')",
			)
			if err != nil {
				t.Fatal(err)
			}

			for range 2 {
				var count int

				err = database.GetContext(
					t.Context(),
					&count,
					"SELECT COUNT(*) FROM holdings",
				)
				if err != nil {
					t.Fatal(err)
				}

				if count != 1 {
					t.Fatalf("memory database lost rows: %d", count)
				}
			}
		})
	}
}
