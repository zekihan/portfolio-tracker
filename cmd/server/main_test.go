package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestSnapshotScheduleHourly(t *testing.T) {
	t.Parallel()

	schedule, err := cron.NewParser(cron.Second | cron.Minute | cron.Hour |
		cron.Dom | cron.Month | cron.Dow | cron.Descriptor).Parse(snapshotSchedule)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, time.October, 12, 10, 0, 0, 0, time.UTC)

	want := start.Add(time.Hour)
	for _, now := range []time.Time{start, start.Add(time.Minute), want.Add(-time.Second)} {
		if got := schedule.Next(now); !got.Equal(want) {
			t.Errorf("next snapshot after %v = %v, want %v", now, got, want)
		}
	}
}

func TestSnapshotLockEmptyPortfolio(t *testing.T) {
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

	if !acquireSnapshotLock(first) {
		t.Fatal("first instance did not acquire the lock")
	}

	if acquireSnapshotLock(second) {
		t.Fatal("second instance acquired an already-held lock on an empty portfolio")
	}

	releaseSnapshotLock(first)

	if !acquireSnapshotLock(second) {
		t.Fatal("second instance did not acquire the released lock")
	}

	releaseSnapshotLock(second)
}

func TestSnapshotLockClosedDatabase(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	err = database.Close()
	if err != nil {
		t.Fatal(err)
	}

	if acquireSnapshotLock(database) {
		t.Fatal("acquired a snapshot lock with a closed database")
	}
}

func TestSnapshotLockReclaimsStaleLock(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "portfolio.db")

	first, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = first.Close() })

	if !acquireSnapshotLock(first) {
		t.Fatal("first instance did not acquire the lock")
	}

	// Simulate a crash: the holder died without releasing the lock.
	// Backdate the lock so it appears older than snapshotLockTimeout.
	_, err = first.Exec(`UPDATE snapshot_lock SET acquired_at = ? WHERE id = 1`,
		time.Now().UTC().Add(-2*snapshotLockTimeout))
	if err != nil {
		t.Fatal(err)
	}

	second, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = second.Close() })

	if !acquireSnapshotLock(second) {
		t.Fatal("second instance did not reclaim the stale lock")
	}

	releaseSnapshotLock(second)
}
