package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestOpenConfiguresReplacementConnections(t *testing.T) {
	t.Parallel()

	database, err := Open("file:" + filepath.Join(t.TempDir(), "portfolio.db") +
		"?_foreign_keys=off&_busy_timeout=1")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })
	database.SetMaxIdleConns(0)

	for range 2 {
		var foreignKeys, busyTimeout int

		err = database.GetContext(t.Context(), &foreignKeys, "PRAGMA foreign_keys")
		if err != nil {
			t.Fatal(err)
		}

		err = database.GetContext(t.Context(), &busyTimeout, "PRAGMA busy_timeout")
		if err != nil {
			t.Fatal(err)
		}

		if foreignKeys != 1 || busyTimeout != 5000 {
			t.Fatalf(
				"connection settings: foreign_keys=%d, busy_timeout=%d",
				foreignKeys,
				busyTimeout,
			)
		}
	}
}

func TestOpenPreservesExistingTimestamps(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "portfolio.db")

	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.ExecContext(t.Context(), `
		INSERT INTO holdings (name, asset_type, quantity, created_at, updated_at)
		VALUES ('Legacy holding', 'Cash', 42,
			'2026-10-12 09:10:11.123456789+03:00', '2026-10-12 09:10:11')`)
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

	var row struct {
		CreatedAt time.Time `db:"created_at"`
		UpdatedAt time.Time `db:"updated_at"`
		Quantity  float64   `db:"quantity"`
	}

	err = reopened.GetContext(
		t.Context(),
		&row,
		"SELECT quantity, created_at, updated_at FROM holdings WHERE name = 'Legacy holding'",
	)
	if err != nil {
		t.Fatal(err)
	}

	wantCreated := time.Date(2026, time.October, 12, 9, 10, 11, 123456789,
		time.FixedZone("", 3*60*60))

	wantUpdated := time.Date(2026, time.October, 12, 9, 10, 11, 0, time.UTC)
	if row.Quantity != 42 || !row.CreatedAt.Equal(wantCreated) ||
		!row.UpdatedAt.Equal(wantUpdated) {
		t.Fatalf("existing holding changed: %+v", row)
	}

	_, err = reopened.ExecContext(t.Context(),
		"UPDATE holdings SET manual_price_at = ? WHERE name = 'Legacy holding'", wantCreated)
	if err != nil {
		t.Fatal(err)
	}

	var manualPriceAt time.Time

	err = reopened.GetContext(t.Context(), &manualPriceAt,
		"SELECT manual_price_at FROM holdings WHERE name = 'Legacy holding'")
	if err != nil {
		t.Fatal(err)
	}

	if !manualPriceAt.Equal(wantCreated) {
		t.Fatalf("timestamp round trip = %v, want %v", manualPriceAt, wantCreated)
	}
}
