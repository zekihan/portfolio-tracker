package services

import (
	"database/sql"
	"fmt"
)

func wrapOperationError(operation string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("%s: %w", operation, err)
}

// holdingMutationResult reports absence rather than acknowledging a no-op write.
func holdingMutationResult(operation string, result sql.Result, err error) error {
	if err != nil {
		return wrapOperationError(operation, err)
	}

	count, err := result.RowsAffected()
	if err != nil {
		return wrapOperationError(operation, err)
	}

	if count == 0 {
		return wrapOperationError(operation, sql.ErrNoRows)
	}

	return nil
}

// IncompleteValuationError prevents partial totals from being stored as portfolio snapshots.
type IncompleteValuationError struct {
	UnvaluedCount int
	TotalHoldings int
}

func (err *IncompleteValuationError) Error() string {
	return fmt.Sprintf(
		"snapshot skipped: %d of %d holdings cannot be valued",
		err.UnvaluedCount,
		err.TotalHoldings,
	)
}
