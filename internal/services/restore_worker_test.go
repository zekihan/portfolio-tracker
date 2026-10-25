package services

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestRestoreUntrustedValidationDoesNotAcquireLiveConnection(t *testing.T) {
	t.Parallel()
	source := openTestDB(t, "source.db")
	target := openTestDB(t, "target.db")
	seedRestoreValidationData(t, source, "Backup")
	seedRestoreValidationData(t, target, "Live")
	execRestoreFixture(t, source, `UPDATE snapshots SET holdings_json = '[{"id":1,"unknown":1}]'`)
	backup := filepath.Join(t.TempDir(), "backup.db")

	err := source.Backup(t.Context(), backup)
	if err != nil {
		t.Fatal(err)
	}

	conn, err := target.db.Connx(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = conn.Close() }()
	// Keep the only live connection in use while the real worker rejects the
	// upload. Validation in the server would block here until the timeout.
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	result := make(chan error, 1)
	go func() {
		result <- target.RestoreFromWithGuard(ctx, backup, func(context.Context, func() error) error {
			return context.Canceled
		})
	}()

	var count int

	err = conn.GetContext(ctx, &count, "SELECT COUNT(*) FROM holdings WHERE name = 'Live'")
	if err != nil || count != 1 {
		t.Fatalf("live query during validation = %d, %v", count, err)
	}

	err = <-result
	if !errors.Is(err, ErrIncompatibleBackup) {
		t.Fatalf("restore held live connection: %v", err)
	}

	err = conn.GetContext(ctx, &count, "SELECT COUNT(*) FROM holdings WHERE name = 'Live'")
	if err != nil || count != 1 {
		t.Fatalf("live query after validation = %d, %v", count, err)
	}
}
