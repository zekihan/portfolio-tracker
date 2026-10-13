package services

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

type snapshotBlockedFX struct {
	entered chan struct{}
	release chan struct{}
}

func (getter *snapshotBlockedFX) Name() string       { return "Frankfurter" }
func (getter *snapshotBlockedFX) Type() string       { return "FX" }
func (getter *snapshotBlockedFX) TTL() time.Duration { return time.Hour }

func (getter *snapshotBlockedFX) GetPrice(
	ctx context.Context,
	instrument string,
) (models.Price, error) {
	if instrument == "EUR" {
		return models.Price{Value: 50, Currency: "TRY", Source: "test"}, nil
	}

	close(getter.entered)

	select {
	case <-getter.release:
		return models.Price{Value: 40, Currency: "TRY", FetchedAt: time.Now(), Source: "test"}, nil
	case <-ctx.Done():
		return models.Price{}, wrapOperationError("test FX", ctx.Err())
	}
}

func TestSnapshotCannotOutliveDataReplacement(t *testing.T) {
	t.Parallel()

	for _, operation := range []string{"clear", "history", "restore", "stolen lease"} {
		t.Run(operation, func(t *testing.T) {
			runSnapshotReplacement(t, operation)
		})
	}
}

func runSnapshotReplacement(t *testing.T, operation string) {
	t.Helper()
	t.Parallel()
	path := filepath.Join(t.TempDir(), "portfolio.db")

	first := openSnapshotDatabase(t, path)
	second := openSnapshotDatabase(t, path)

	getter := &snapshotBlockedFX{entered: make(chan struct{}), release: make(chan struct{})}
	registry := price.NewRegistry()
	registry.Register(getter)
	prices := price.NewService(first, registry)

	t.Cleanup(func() { _ = prices.Close() })

	holdings := NewHoldingService(first, prices)
	holding := models.Holding{
		Name:         "Old cash",
		AssetType:    models.AssetTypeCash,
		BaseCurrency: "TRY",
		Quantity:     1,
		ManualPrice:  sql.NullFloat64{Float64: 100, Valid: true},
	}

	err := holdings.Create(t.Context(), &holding)
	if err != nil {
		t.Fatal(err)
	}

	service := NewSnapshotService(first, prices, holdings)
	settings := NewSettingsService(second)

	backupPath := filepath.Join(t.TempDir(), "backup.db")
	if operation == "restore" {
		openSnapshotDatabase(t, backupPath)
	}

	ctx := t.Context()
	if operation == "stolen lease" {
		_, err = first.ExecContext(
			ctx,
			"INSERT INTO snapshot_runs (slot, owner, acquired_at) VALUES (1, 'old', 0)",
		)
		if err != nil {
			t.Fatal(err)
		}

		ctx = db.WithSnapshotClaim(ctx, 1, "old")
	}

	done := make(chan error, 1)
	go func() { done <- service.CreateSnapshot(ctx) }()

	select {
	case <-getter.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("snapshot did not reach FX")
	}

	switch operation {
	case "clear":
		err = settings.ClearAllData(t.Context())
	case "history":
		err = NewSnapshotService(second, nil, nil).ClearHistory(t.Context())
	case "restore":
		err = settings.RestoreFrom(t.Context(), backupPath)
	case "stolen lease":
		_, err = second.ExecContext(
			t.Context(),
			"UPDATE snapshot_runs SET owner = 'new' WHERE slot = 1",
		)
	}

	close(getter.release)

	if err != nil {
		t.Fatal(err)
	}

	assertSnapshotSuperseded(t, first, done)
}

func assertSnapshotSuperseded(t *testing.T, first *sqlx.DB, done <-chan error) {
	t.Helper()

	select {
	case snapshotErr := <-done:
		if snapshotErr == nil {
			t.Fatalf("snapshot error = %v", snapshotErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("snapshot did not finish")
	}

	var count int

	err := first.GetContext(
		t.Context(),
		&count,
		"SELECT COUNT(*) FROM snapshots",
	)
	if err != nil {
		t.Fatal(err)
	}

	if count != 0 {
		t.Fatalf("persisted %d obsolete snapshots", count)
	}
}

func openSnapshotDatabase(t *testing.T, path string) *sqlx.DB {
	t.Helper()

	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	return database
}

func TestScheduledSnapshotCompletionIsAtomicAndManualIndependent(t *testing.T) {
	t.Parallel()
	database := valuationDatabase(t)
	seedEURQuote(t, database)
	prices := price.NewService(database, price.DefaultRegistry())

	t.Cleanup(func() { _ = prices.Close() })

	service := NewSnapshotService(database, prices, NewHoldingService(database, prices))

	_, err := database.ExecContext(t.Context(),
		"INSERT INTO snapshot_runs (slot, owner, acquired_at) VALUES (1, 'scheduled', 0)")
	if err != nil {
		t.Fatal(err)
	}

	ctx := db.WithSnapshotClaim(t.Context(), 1, "scheduled")

	err = service.CreateSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var completed int

	err = database.GetContext(
		t.Context(), &completed, "SELECT completed FROM snapshot_runs WHERE slot = 1",
	)
	if err != nil || completed != 1 {
		t.Fatalf("completion missing after insertion: %d, %v", completed, err)
	}

	err = service.CreateSnapshot(ctx)
	if !errors.Is(err, db.ErrSnapshotSuperseded) {
		t.Fatalf("completed slot inserted again: %v", err)
	}

	err = service.CreateSnapshot(t.Context())
	if err != nil {
		t.Fatalf("manual snapshot blocked by completed hourly slot: %v", err)
	}

	var count int

	err = database.GetContext(t.Context(), &count, "SELECT COUNT(*) FROM snapshots")
	if err != nil || count != 2 {
		t.Fatalf("snapshot count = %d, error = %v", count, err)
	}
}

func TestSnapshotPersistenceFencesEpochAndRollsBackCompletionFailure(t *testing.T) {
	t.Parallel()
	database := openSnapshotDatabase(t, filepath.Join(t.TempDir(), "portfolio.db"))
	service := NewSnapshotService(database, nil, nil)
	snapshot := &models.Snapshot{USDTRYRate: 40, HoldingsJSON: "[]"}

	_, err := database.ExecContext(t.Context(),
		"UPDATE snapshot_state SET generation = generation + 1 WHERE id = 1")
	if err != nil {
		t.Fatal(err)
	}

	err = service.persistSnapshot(t.Context(), 0, snapshot)
	if !errors.Is(err, db.ErrSnapshotSuperseded) {
		t.Fatalf("old generation accepted: %v", err)
	}

	_, err = database.ExecContext(t.Context(), `
		INSERT INTO snapshot_runs (slot, owner, acquired_at) VALUES (1, 'scheduled', 0);
		CREATE TRIGGER reject_completion BEFORE UPDATE OF completed ON snapshot_runs
		BEGIN SELECT RAISE(ABORT, 'injected completion failure'); END;`)
	if err != nil {
		t.Fatal(err)
	}

	err = service.persistSnapshot(db.WithSnapshotClaim(t.Context(), 1, "scheduled"), 1, snapshot)
	if err == nil {
		t.Fatal("snapshot persisted despite failed completion")
	}

	var count int

	err = database.GetContext(t.Context(), &count, "SELECT COUNT(*) FROM snapshots")
	if err != nil || count != 0 {
		t.Fatalf("partial snapshot committed: %d, %v", count, err)
	}

	err = database.GetContext(
		t.Context(), &count, "SELECT completed FROM snapshot_runs WHERE slot = 1",
	)
	if err != nil || count != 0 {
		t.Fatalf("failed slot marked completed: %d, %v", count, err)
	}
}
