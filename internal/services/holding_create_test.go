package services

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
)

func createReceiptHolding() models.Holding {
	return models.Holding{
		Name: "Cash", AssetType: models.AssetTypeCash, BaseCurrency: "TRY",
		Quantity: 5, ManualPrice: sql.NullFloat64{Float64: 1, Valid: true},
	}
}

func TestCreateReceiptSurvivesHistoryClear(t *testing.T) {
	t.Parallel()
	settings := openTestDB(t, "portfolio.db")
	service := NewHoldingService(settings.db, nil)
	snapshots := NewSnapshotService(settings.db, nil, nil)
	original := createReceiptHolding()
	ctx := db.WithPortfolioGeneration(t.Context(), 0)

	err := service.CreateWithIdempotencyKey(ctx, &original, "history-key")
	if err != nil {
		t.Fatal(err)
	}

	for generation := range int64(3) {
		err = snapshots.ClearHistory(ctx)
		if err != nil {
			t.Fatal(err)
		}

		retry := createReceiptHolding()

		err = service.CreateWithIdempotencyKey(ctx, &retry, "history-key")
		if !errors.Is(err, db.ErrPortfolioChanged) {
			t.Fatalf("stale generation retry = %v", err)
		}

		ctx = db.WithPortfolioGeneration(t.Context(), generation+1)

		err = service.CreateWithIdempotencyKey(ctx, &retry, "history-key")
		if err != nil {
			t.Fatal(err)
		}

		if !reflect.DeepEqual(original, retry) {
			t.Fatalf("history clear lost receipt: original=%+v retry=%+v", original, retry)
		}

		retry.Quantity++

		err = service.CreateWithIdempotencyKey(ctx, &retry, "history-key")
		if !errors.Is(err, ErrHoldingIdempotencyConflict) {
			t.Fatalf("changed payload after history clear = %v", err)
		}
	}

	if got := countRows(t, settings, "SELECT COUNT(*) FROM holdings"); got != 1 {
		t.Fatalf("history clear allowed duplicate holdings: %d", got)
	}
}

func TestHistoryClearKeepsObsoleteReceiptsUnreachable(t *testing.T) {
	t.Parallel()
	settings := openTestDB(t, "portfolio.db")
	service := NewHoldingService(settings.db, nil)
	original := createReceiptHolding()

	err := service.CreateWithIdempotencyKey(t.Context(), &original, "old-key")
	if err != nil {
		t.Fatal(err)
	}

	// Model a history clear performed before receipts were carried forward.
	_, err = settings.db.ExecContext(t.Context(),
		"UPDATE snapshot_state SET generation = 1 WHERE id = 1")
	if err != nil {
		t.Fatal(err)
	}

	current := createReceiptHolding()

	err = service.CreateWithIdempotencyKey(t.Context(), &current, "old-key")
	if err != nil {
		t.Fatal(err)
	}

	err = NewSnapshotService(settings.db, nil, nil).ClearHistory(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	retry := createReceiptHolding()

	err = service.CreateWithIdempotencyKey(t.Context(), &retry, "old-key")
	if err != nil || !reflect.DeepEqual(current, retry) {
		t.Fatalf("current receipt was lost or replaced by obsolete receipt: %+v, %v", retry, err)
	}
}

func TestCreateReceiptSurvivesRestartAndDeletion(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "portfolio.db")

	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	service := NewHoldingService(database, nil)
	original := createReceiptHolding()

	ctx := db.WithPortfolioGeneration(t.Context(), 0)

	err = service.CreateWithIdempotencyKey(ctx, &original, "retry-key")
	if err != nil {
		t.Fatal(err)
	}

	err = database.Close()
	if err != nil {
		t.Fatal(err)
	}

	database, err = db.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	service = NewHoldingService(database, nil)

	for _, deleted := range []bool{false, true} {
		if deleted {
			err = service.Delete(ctx, original.ID)
			if err != nil {
				t.Fatal(err)
			}
		}

		retry := createReceiptHolding()

		err = service.CreateWithIdempotencyKey(ctx, &retry, "retry-key")
		if err != nil {
			t.Fatal(err)
		}

		if !reflect.DeepEqual(original, retry) {
			t.Fatalf("replay differs: original=%+v retry=%+v", original, retry)
		}
	}

	rows, err := service.GetAll(ctx)
	if err != nil || len(rows) != 0 {
		t.Fatalf("retry resurrected deleted holding: %v %v", rows, err)
	}
}

func TestCreateReceiptRejectsChangedPayloadAndAllowsDistinctKeys(t *testing.T) {
	t.Parallel()
	settings := openTestDB(t, "portfolio.db")
	service := NewHoldingService(settings.db, nil)
	ctx := db.WithPortfolioGeneration(t.Context(), 0)

	original := createReceiptHolding()

	err := service.CreateWithIdempotencyKey(ctx, &original, "same-key")
	if err != nil {
		t.Fatal(err)
	}

	changed := createReceiptHolding()

	changed.Quantity = 7

	err = service.CreateWithIdempotencyKey(
		ctx,
		&changed,
		"same-key",
	)
	if !errors.Is(
		err,
		ErrHoldingIdempotencyConflict,
	) {
		t.Fatalf("changed request error = %v", err)
	}

	if got := countRows(t, settings, "SELECT quantity FROM holdings"); got != 5 {
		t.Fatalf("collision changed holding quantity to %d", got)
	}

	for _, key := range []string{"distinct-key", "", ""} {
		holding := createReceiptHolding()

		err := service.CreateWithIdempotencyKey(ctx, &holding, key)
		if err != nil {
			t.Fatal(err)
		}
	}

	if got := countRows(t, settings, "SELECT COUNT(*) FROM holdings"); got != 4 {
		t.Fatalf("distinct keys/missing keys must allow identical holdings, count=%d", got)
	}
}

func TestCreateReceiptConcurrentConnections(t *testing.T) {
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

	services := []*HoldingService{NewHoldingService(first, nil), NewHoldingService(second, nil)}

	const attempts = 12

	results := make([]models.Holding, attempts)
	failures := make([]error, attempts)

	var workers sync.WaitGroup
	for index := range attempts {
		workers.Go(func() {
			results[index] = createReceiptHolding()
			failures[index] = services[index%len(services)].CreateWithIdempotencyKey(
				db.WithPortfolioGeneration(t.Context(), 0), &results[index], "concurrent-key")
		})
	}

	workers.Wait()

	for index := range attempts {
		if failures[index] != nil || !reflect.DeepEqual(results[0], results[index]) {
			t.Fatalf("concurrent create %d = %+v, %v", index, results[index], failures[index])
		}
	}

	rows, err := services[0].GetAll(t.Context())
	if err != nil || len(rows) != 1 {
		t.Fatalf("concurrent creates = %d, %v", len(rows), err)
	}
}

func TestCreateReceiptFailureRollsBackHolding(t *testing.T) {
	t.Parallel()
	settings := openTestDB(t, "portfolio.db")
	service := NewHoldingService(settings.db, nil)

	_, err := settings.db.ExecContext(t.Context(), `CREATE TRIGGER reject_receipt
		BEFORE INSERT ON holding_create_receipts BEGIN SELECT RAISE(ABORT, 'receipt failed'); END`)
	if err != nil {
		t.Fatal(err)
	}

	holding := createReceiptHolding()

	err = service.CreateWithIdempotencyKey(t.Context(), &holding, "atomic-key")
	if err == nil {
		t.Fatal("receipt failure did not abort Create")
	}

	if got := countRows(t, settings, "SELECT COUNT(*) FROM holdings"); got != 0 {
		t.Fatalf("failed receipt left %d holdings", got)
	}

	_, err = settings.db.ExecContext(t.Context(), "DROP TRIGGER reject_receipt")
	if err != nil {
		t.Fatal(err)
	}

	err = service.CreateWithIdempotencyKey(t.Context(), &holding, "atomic-key")
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateReceiptFencesRestoreAndClear(t *testing.T) {
	t.Parallel()

	for _, operation := range []string{"restore9", "restore10", "restore11", "restore12", "clear"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			target := openTestDB(t, "target.db")
			service := NewHoldingService(target.db, nil)
			original := createReceiptHolding()

			ctx := db.WithPortfolioGeneration(t.Context(), 0)

			err := service.CreateWithIdempotencyKey(ctx, &original, "local-key")
			if err != nil {
				t.Fatal(err)
			}

			if operation == "clear" {
				err = target.ClearAllData(ctx)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				restoreReceiptFixture(t, target, operation)
			}

			if got := countRows(
				t,
				target,
				"SELECT COUNT(*) FROM holding_create_receipts",
			); got != 0 {
				t.Fatalf("replacement retained or imported %d receipts", got)
			}

			retry := createReceiptHolding()

			err = service.CreateWithIdempotencyKey(
				ctx,
				&retry,
				"local-key",
			)
			if !errors.Is(
				err,
				db.ErrPortfolioChanged,
			) {
				t.Fatalf("stale retry after replacement = %v", err)
			}

			err = service.CreateWithIdempotencyKey(
				db.WithPortfolioGeneration(t.Context(), 1),
				&retry,
				"local-key",
			)
			if err != nil {
				t.Fatal(err)
			}

			if retry.ID == original.ID || retry.CreatedAt.Equal(original.CreatedAt) {
				t.Fatal("fresh generation replayed pre-replacement response")
			}
		})
	}
}

func restoreReceiptFixture(t *testing.T, target *SettingsService, operation string) {
	t.Helper()
	source := openTestDB(t, "source.db")
	service := NewHoldingService(source.db, nil)
	holding := createReceiptHolding()

	holding.Name = "Backup holding"

	err := service.CreateWithIdempotencyKey(t.Context(), &holding, "backup-key")
	if err != nil {
		t.Fatal(err)
	}

	version, err := strconv.Atoi(operation[len("restore"):])
	if err != nil {
		t.Fatal(err)
	}

	if version < restoreEURVersion {
		useLegacyRestoreSchema(t, source, version)
	}

	if version < 11 {
		_, err = source.db.ExecContext(
			t.Context(),
			"DROP TABLE IF EXISTS holding_create_receipts",
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	if version < 10 {
		_, err = source.db.ExecContext(
			t.Context(),
			"DROP TABLE IF EXISTS snapshot_runs; DROP TABLE IF EXISTS snapshot_state",
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	backup := filepath.Join(t.TempDir(), "backup.db")

	err = source.Backup(t.Context(), backup)
	if err != nil {
		t.Fatal(err)
	}

	err = target.RestoreFrom(db.WithPortfolioGeneration(t.Context(), 0), backup)
	if err != nil {
		t.Fatal(err)
	}
}
