package services

import (
	"path/filepath"
	"testing"
)

func TestBackupRestorePreservesNativeFilename(t *testing.T) {
	t.Parallel()
	source := openTestDB(t, "source.db")
	target := openTestDB(t, "target.db")

	err := source.SetDisplayCurrency(t.Context(), "USD")
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "backup owner #100%20.db")

	err = source.Backup(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	err = target.restoreForTest(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	settings, err := target.GetAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if settings.DisplayCurrency != "USD" {
		t.Fatalf("restored wrong file: %+v", settings)
	}
}

func TestBackupRestoreWithRelativeTempDir(t *testing.T) {
	source := openTestDB(t, "source.db")
	target := openTestDB(t, "target.db")
	seedRestoreValidationData(t, source, "Backup")
	seedRestoreValidationData(t, target, "Live")
	path := filepath.Join(t.TempDir(), "backup.db")

	err := source.Backup(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	t.Chdir(t.TempDir())
	t.Setenv("TMPDIR", ".")

	err = target.restoreForTest(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	if got := countRows(
		t,
		target,
		"SELECT COUNT(*) FROM holdings WHERE name = 'Backup'",
	); got != 1 {
		t.Fatalf("restored holdings = %d, want 1", got)
	}
}
