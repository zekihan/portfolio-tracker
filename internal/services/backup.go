package services

import (
	"context"
	"fmt"
)

// Backup writes a consistent copy of the database to path. The path must not
// already exist; SQLite's VACUUM INTO creates the file.
func (service *SettingsService) Backup(ctx context.Context, path string) error {
	_, err := service.db.ExecContext(ctx, "VACUUM INTO ?", path)
	if err != nil {
		return fmt.Errorf("write backup: %w", err)
	}

	return nil
}
