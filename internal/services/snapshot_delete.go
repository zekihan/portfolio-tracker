package services

import (
	"context"
	"database/sql"
	"fmt"
)

// DeleteSnapshot removes one historical valuation without changing current holdings.
func (service *SnapshotService) DeleteSnapshot(ctx context.Context, id int) error {
	result, err := service.db.ExecContext(ctx, "DELETE FROM snapshots WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete snapshot: %w", err)
	}

	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete snapshot result: %w", err)
	}

	if count == 0 {
		return fmt.Errorf("delete snapshot: %w", sql.ErrNoRows)
	}

	return nil
}

// ClearHistory removes every snapshot and fences valuations started before the
// clear. Current holdings, configuration, cache, and scheduler claims are kept.
func (service *SnapshotService) ClearHistory(ctx context.Context) error {
	tx, err := service.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin clear history: %w", err)
	}

	defer func() {
		//nolint:errcheck // Rollback after a successful commit is harmless.
		_ = tx.Rollback()
	}()

	for _, statement := range []string{
		"UPDATE snapshot_state SET generation = generation + 1 WHERE id = 1",
		"DELETE FROM snapshots",
	} {
		_, err = tx.ExecContext(ctx, statement)
		if err != nil {
			return fmt.Errorf("clear history: %w", err)
		}
	}

	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("commit clear history: %w", err)
	}

	return nil
}
