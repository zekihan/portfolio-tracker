package db

import (
	"path/filepath"
	"testing"
)

func TestGetSetSettingRoundtrip(t *testing.T) {
	t.Parallel()

	database, err := Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	value, err := GetSetting(database, "missing_key")
	if err != nil {
		t.Fatal(err)
	}

	if value != "" {
		t.Errorf("missing setting = %q, want empty", value)
	}

	err = SetSetting(database, "display_currency", "EUR")
	if err != nil {
		t.Fatal(err)
	}

	value, err = GetSetting(database, "display_currency")
	if err != nil {
		t.Fatal(err)
	}

	if value != "EUR" {
		t.Errorf("setting = %q, want EUR", value)
	}

	// Upsert overwrites the seeded value instead of inserting a duplicate.
	err = SetSetting(database, "display_currency", "USD")
	if err != nil {
		t.Fatal(err)
	}

	value, err = GetSettingContext(t.Context(), database, "display_currency")
	if err != nil {
		t.Fatal(err)
	}

	if value != "USD" {
		t.Errorf("setting after upsert = %q, want USD", value)
	}

	var count int

	err = database.GetContext(t.Context(), &count,
		"SELECT COUNT(*) FROM settings WHERE key = 'display_currency'")
	if err != nil {
		t.Fatal(err)
	}

	if count != 1 {
		t.Errorf("setting rows = %d, want 1", count)
	}
}

func TestReadPortfolioGenerationAndSnapshotClaim(t *testing.T) {
	t.Parallel()

	database, err := Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	generation, err := ReadPortfolioGeneration(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}

	if generation != 0 {
		t.Errorf("initial generation = %d, want 0", generation)
	}

	_, err = database.ExecContext(t.Context(),
		"UPDATE snapshot_state SET generation = generation + 1 WHERE id = 1")
	if err != nil {
		t.Fatal(err)
	}

	generation, err = ReadPortfolioGeneration(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}

	if generation != 1 {
		t.Errorf("generation after update = %d, want 1", generation)
	}

	slot, owner := SnapshotClaim(t.Context())
	if slot != 0 || owner != "" {
		t.Errorf("manual claim = (%d, %q), want (0, \"\")", slot, owner)
	}

	slot, owner = SnapshotClaim(WithSnapshotClaim(t.Context(), 42, "owner-1"))
	if slot != 42 || owner != "owner-1" {
		t.Errorf("scheduled claim = (%d, %q), want (42, \"owner-1\")", slot, owner)
	}
}
