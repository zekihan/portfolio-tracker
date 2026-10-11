package services

import (
	"path/filepath"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestBackupCopiesDatabase(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	database, err := db.Open(filepath.Join(dir, "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.ExecContext(
		t.Context(),
		`INSERT INTO holdings (name, asset_type) VALUES ('Stock', 'Stock')`,
	)
	if err != nil {
		t.Fatal(err)
	}

	backupPath := filepath.Join(dir, "backup.db")

	err = NewSettingsService(database).Backup(t.Context(), backupPath)
	if err != nil {
		t.Fatal(err)
	}

	copied, err := db.Open(backupPath)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = copied.Close() })

	var count int

	err = copied.GetContext(t.Context(), &count, "SELECT COUNT(*) FROM holdings")
	if err != nil {
		t.Fatal(err)
	}

	if count != 1 {
		t.Errorf("backup holdings = %d, want 1", count)
	}
}
