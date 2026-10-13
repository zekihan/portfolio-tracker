package db

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestPortfolioGuardHoldsWriterLockThroughMutation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "portfolio.db")

	writer, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = writer.Close() })

	replica, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = replica.Close() })

	_, err = replica.ExecContext(t.Context(), "PRAGMA busy_timeout = 1")
	if err != nil {
		t.Fatal(err)
	}

	ctx := WithPortfolioGeneration(t.Context(), 0)

	tx, err := writer.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		_ = tx.Rollback()
	}()

	err = CheckPortfolioGeneration(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	// A different process cannot replace data between the guard and the write.
	_, err = replica.ExecContext(
		t.Context(),
		"UPDATE snapshot_state SET generation = generation + 1 WHERE id = 1",
	)
	if err == nil {
		t.Fatal("replacement acquired writer lock between guard and mutation")
	}

	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO holdings (name, asset_type) VALUES ('Before replacement', 'Cash')`,
	)
	if err != nil {
		t.Fatal(err)
	}

	err = tx.Commit()
	if err != nil {
		t.Fatal(err)
	}

	_, err = replica.ExecContext(
		t.Context(),
		"UPDATE snapshot_state SET generation = generation + 1 WHERE id = 1",
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = ExecPortfolioMutation(ctx, writer, "DELETE FROM holdings WHERE id = 1")
	if !errors.Is(err, ErrPortfolioChanged) {
		t.Fatalf("old generation error = %v", err)
	}

	var count int

	err = writer.GetContext(t.Context(), &count, "SELECT COUNT(*) FROM holdings")
	if err != nil || count != 1 {
		t.Fatalf("stale write changed holdings: count=%d err=%v", count, err)
	}
}
