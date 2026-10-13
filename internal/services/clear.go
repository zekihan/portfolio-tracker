package services

import (
	"context"
	"fmt"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

// ClearAllData deletes holdings, snapshots, and cached prices, and resets
// settings to their defaults, in a single transaction. Provider configuration
// (provider_registry) is kept.
func (service *SettingsService) ClearAllData(ctx context.Context) error {
	tx, err := service.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin clear all data: %w", err)
	}

	defer func() {
		//nolint:errcheck // Rollback after a successful commit returns an error that is not actionable
		_ = tx.Rollback()
	}()

	err = db.CheckPortfolioGeneration(ctx, tx)
	if err != nil {
		return wrapOperationError("check portfolio generation", err)
	}

	statements := []string{
		"UPDATE snapshot_state SET generation = generation + 1 WHERE id = 1",
		"DELETE FROM holding_create_receipts",
		"DELETE FROM holdings",
		"DELETE FROM snapshots",
		"DELETE FROM price_cache",
		"DELETE FROM settings",
		`INSERT INTO settings (key, value) VALUES
			('display_currency', 'TRY'),
			('dark_mode', 'system'),
			('refresh_interval', '60')`,
	}

	for _, statement := range statements {
		_, err = tx.ExecContext(ctx, statement)
		if err != nil {
			return fmt.Errorf("clear all data: %w", err)
		}
	}

	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("commit clear all data: %w", err)
	}

	return nil
}
