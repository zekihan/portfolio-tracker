package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// ErrPortfolioChanged rejects client actions based on data from before a replacement.
var ErrPortfolioChanged = errors.New("portfolio changed; reload the page before retrying")

type portfolioGenerationKey struct{}

// WithPortfolioGeneration carries a client's displayed-data generation to a mutation.
func WithPortfolioGeneration(ctx context.Context, generation int64) context.Context {
	return context.WithValue(ctx, portfolioGenerationKey{}, generation)
}

// PortfolioGeneration returns the client precondition, if supplied by the HTTP boundary.
func PortfolioGeneration(ctx context.Context) (int64, bool) {
	generation, ok := ctx.Value(portfolioGenerationKey{}).(int64)

	return generation, ok
}

// ReadPortfolioGeneration must run before reading data that will be shown to a client.
func ReadPortfolioGeneration(ctx context.Context, database *sqlx.DB) (int64, error) {
	var generation int64

	err := database.GetContext(
		ctx,
		&generation,
		"SELECT generation FROM snapshot_state WHERE id = 1",
	)
	if err != nil {
		return 0, fmt.Errorf("read portfolio generation: %w", err)
	}

	return generation, nil
}

// CheckPortfolioGeneration obtains the SQLite writer lock while checking the
// precondition. The caller must keep this transaction through the mutation;
// a separate read/check would allow a restore to race between check and write.
// Calls without a precondition are trusted internal operations, never HTTP writes.
func CheckPortfolioGeneration(ctx context.Context, tx *sqlx.Tx) error {
	generation, ok := PortfolioGeneration(ctx)
	if !ok {
		return nil
	}

	result, err := tx.ExecContext(
		ctx,
		"UPDATE snapshot_state SET generation = generation WHERE id = 1 AND generation = ?",
		generation,
	)
	if err != nil {
		return fmt.Errorf("check portfolio generation: %w", err)
	}

	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check portfolio generation result: %w", err)
	}

	if count != 1 {
		return ErrPortfolioChanged
	}

	return nil
}

// ExecPortfolioMutation atomically fences a single-statement client mutation.
func ExecPortfolioMutation(
	ctx context.Context,
	database *sqlx.DB,
	query string,
	args ...any,
) (sql.Result, error) {
	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin portfolio mutation: %w", err)
	}
	defer func() {
		//nolint:errcheck // Rollback after a successful commit is harmless.
		_ = tx.Rollback()
	}()

	err = CheckPortfolioGeneration(ctx, tx)
	if err != nil {
		return nil, err
	}

	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("write portfolio mutation: %w", err)
	}

	err = tx.Commit()
	if err != nil {
		return nil, fmt.Errorf("commit portfolio mutation: %w", err)
	}

	return result, nil
}
