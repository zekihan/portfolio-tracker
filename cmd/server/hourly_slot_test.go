package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestScheduledSnapshotLateReplicaAndFailureRetry(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "portfolio.db")

	first, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = first.Close() })

	second, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = second.Close() })

	now := time.Date(2026, time.October, 12, 10, 0, 0, 0, time.UTC)
	calls := 0
	create := func(context.Context) error {
		calls++

		return nil
	}
	createScheduledSnapshotAt(t.Context(), first, create, now)
	createScheduledSnapshotAt(t.Context(), second, create, now.Add(20*time.Minute))

	if calls != 1 {
		t.Fatalf("same hour ran %d times", calls)
	}

	createScheduledSnapshotAt(t.Context(), second, create, now.Add(time.Hour))

	if calls != 2 {
		t.Fatalf("next hour did not run: %d", calls)
	}

	failedSlot := now.Add(2 * time.Hour)
	createScheduledSnapshotAt(t.Context(), first, func(context.Context) error {
		return errors.New("provider unavailable") //nolint:err113 // Test failure.
	}, failedSlot)
	createScheduledSnapshotAt(t.Context(), second, create, failedSlot.Add(time.Minute))

	if calls != 3 {
		t.Fatalf("failed slot not retried: %d", calls)
	}
}

func TestSnapshotSlotStaleOwnerCannotReleaseNewClaim(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	now := time.Now().UTC()

	slot := now.Truncate(time.Hour).Unix()
	if !claimSnapshotSlot(t.Context(), database, slot, "old", now.Add(-2*snapshotLockTimeout)) {
		t.Fatal("initial claim failed")
	}

	if !claimSnapshotSlot(t.Context(), database, slot, "new", now) {
		t.Fatal("stale claim not reclaimed")
	}

	releaseSnapshotSlot(t.Context(), database, slot, "old")

	var owner string

	err = database.GetContext(
		t.Context(),
		&owner,
		"SELECT owner FROM snapshot_runs WHERE slot = ?",
		slot,
	)
	if err != nil {
		t.Fatal(err)
	}

	if owner != "new" {
		t.Fatalf("new claim lost: %q", owner)
	}
}
