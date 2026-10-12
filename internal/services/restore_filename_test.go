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

	err = target.RestoreFrom(t.Context(), path)
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
