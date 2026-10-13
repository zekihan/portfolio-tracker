package services

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestCanceledRestoreAllowsRetryAndPreservesConnection(t *testing.T) {
	t.Parallel()
	source := openTestDB(t, "source.db")

	_, err := source.db.ExecContext(t.Context(), `WITH RECURSIVE nums(n) AS
 (SELECT 1 UNION ALL SELECT n + 1 FROM nums WHERE n < 1000)
 INSERT INTO holdings(name, asset_type) SELECT 'Backed up', 'Cash' FROM nums`)
	if err != nil {
		t.Fatal(err)
	}

	backup := filepath.Join(t.TempDir(), "backup.db")

	err = source.Backup(t.Context(), backup)
	if err != nil {
		t.Fatal(err)
	}

	for _, memory := range []bool{false, true} {
		name := "file"
		if memory {
			name = "memory"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "live.db")
			if memory {
				path = ":memory:"
			}

			database, openErr := db.Open(path)
			if openErr != nil {
				t.Fatal(openErr)
			}

			t.Cleanup(func() { _ = database.Close() })

			target := NewSettingsService(database)

			restoreErr := target.RestoreFrom(t.Context(), backup)
			if restoreErr != nil {
				t.Fatal(restoreErr)
			}

			checkCanceledRestoreRetries(t, target, backup)
		})
	}
}

func TestDetachRestoreSourceSurvivesCancellation(t *testing.T) {
	t.Parallel()
	target := openTestDB(t, "target.db")

	conn, err := target.db.Connx(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = conn.Close() }()

	_, err = conn.ExecContext(t.Context(), "ATTACH DATABASE ':memory:' AS restore_src")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err = detachRestoreSource(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	// Cleanup also succeeds when ATTACH did not take effect.
	err = detachRestoreSource(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRestoreCleanupFailureClosesDatabase(t *testing.T) {
	t.Parallel()

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	service := NewSettingsService(database)

	conn, err := database.Connx(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = conn.Close() }()

	_, err = conn.ExecContext(
		t.Context(),
		"ATTACH DATABASE ':memory:' AS restore_src; CREATE TABLE restore_src.fixture(id INTEGER)",
	)
	if err != nil {
		t.Fatal(err)
	}

	tx, err := conn.BeginTxx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = tx.Rollback() }()

	var count int

	err = tx.GetContext(t.Context(), &count, "SELECT COUNT(*) FROM restore_src.fixture")
	if err != nil {
		t.Fatal(err)
	}
	// An intentionally held source transaction injects a real DETACH failure.
	err = service.cleanupRestoreConnection(t.Context(), conn)
	if err == nil {
		t.Fatal("expected cleanup failure")
	}

	err = database.PingContext(t.Context())
	if err == nil {
		t.Fatal("failed cleanup left database pool open")
	}
}

func checkCanceledRestoreRetries(t *testing.T, target *SettingsService, backup string) {
	t.Helper()

	canceled := 0

	for attempt := range 12 {
		ctx, cancel := context.WithTimeout(
			t.Context(),
			time.Duration(attempt)*100*time.Microsecond,
		)
		restoreErr := target.RestoreFrom(ctx, backup)

		cancel()

		if errors.Is(restoreErr, context.DeadlineExceeded) {
			canceled++
		} else if restoreErr != nil {
			t.Fatalf("restore failed unexpectedly: %v", restoreErr)
		}

		if count := countRows(t, target, "SELECT COUNT(*) FROM holdings"); count != 1000 {
			t.Fatalf("canceled restore lost live holdings: %d", count)
		}

		if count := countRows(
			t,
			target,
			"SELECT COUNT(*) FROM pragma_database_list WHERE name = 'restore_src'",
		); count != 0 {
			t.Fatal("canceled restore leaked attached source")
		}

		restoreErr = target.RestoreFrom(t.Context(), backup)
		if restoreErr != nil {
			t.Fatalf("retry %d: %v", attempt, restoreErr)
		}
	}

	if canceled == 0 {
		t.Fatal("no cancellation exercised")
	}
}
