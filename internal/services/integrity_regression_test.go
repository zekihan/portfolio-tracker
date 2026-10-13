package services

import (
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestNullableLegacyHoldingsRemainReadable(t *testing.T) {
	t.Parallel()
	source := openTestDB(t, "source.db")
	target := openTestDB(t, "target.db")

	_, err := source.db.ExecContext(
		t.Context(),
		`INSERT INTO holdings (name, asset_type) VALUES ('Legacy', 'Cash')`,
	)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "backup.db")

	err = source.Backup(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	err = target.RestoreFrom(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	service := NewHoldingService(target.db, nil)

	holdings, err := service.GetAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if len(holdings) != 1 || holdings[0].EstPrice != 0 || holdings[0].Ticker != "" {
		t.Fatalf("unexpected holdings: %+v", holdings)
	}

	holding, err := service.GetByID(t.Context(), holdings[0].ID)
	if err != nil || holding == nil {
		t.Fatalf("GetByID = %+v, %v", holding, err)
	}
}

func TestRestorePreservesDeletedHoldingIdentities(t *testing.T) {
	t.Parallel()

	for _, liveID := range []int{0, 100} {
		t.Run(strconv.Itoa(liveID), func(t *testing.T) {
			t.Parallel()
			source := openTestDB(t, "source.db")
			target := openTestDB(t, "target.db")

			_, err := source.db.ExecContext(
				t.Context(),
				`INSERT INTO holdings (id, name, asset_type) VALUES (50, 'Deleted', 'Cash');
INSERT INTO snapshots (total_value_try,total_value_usd,usd_try_rate,holdings_json) VALUES
(40,1,40,'[{"id":50,"value_try":40,"value_usd":1}]'); DELETE FROM holdings`,
			)
			if err != nil {
				t.Fatal(err)
			}

			if liveID > 0 {
				_, err = target.db.ExecContext(
					t.Context(),
					`INSERT INTO holdings (id,name,asset_type) VALUES (?, 'Live','Cash'); DELETE FROM holdings`,
					liveID,
				)
				if err != nil {
					t.Fatal(err)
				}
			}

			path := filepath.Join(t.TempDir(), "backup.db")

			err = source.Backup(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}

			err = target.RestoreFrom(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}

			_, err = target.db.ExecContext(
				t.Context(),
				`INSERT INTO holdings (name,asset_type) VALUES ('New','Cash')`,
			)
			if err != nil {
				t.Fatal(err)
			}

			var id int

			err = target.db.GetContext(t.Context(), &id, "SELECT id FROM holdings")
			if err != nil {
				t.Fatal(err)
			}

			if id <= max(50, liveID) {
				t.Fatalf("new ID %d reused historical high-water mark", id)
			}

			history, err := NewSnapshotService(
				target.db,
				nil,
				nil,
			).GetHoldingHistory(t.Context(), id, 1)
			if err != nil || len(history) != 0 {
				t.Fatalf("new holding inherited history: %+v, %v", history, err)
			}
		})
	}
}

func TestHistoryUsesChronologicalTimestamps(t *testing.T) {
	t.Parallel()
	service := openTestDB(t, "history.db")
	// Both wall-clock strings are yesterday/today but their actual instants straddle the cutoff.
	cutoff := time.Now().UTC().AddDate(0, 0, -1)
	for index, instant := range []time.Time{cutoff.Add(-time.Hour), cutoff.Add(time.Hour)} {
		zone := time.FixedZone("offset", (1-index*2)*12*60*60)
		timestamp := instant.In(zone).Format(time.RFC3339Nano)

		_, err := service.db.ExecContext(t.Context(), `INSERT INTO snapshots
(timestamp,total_value_try,total_value_usd,usd_try_rate,holdings_json) VALUES
(?, ?, 1,40,'[{"id":1,"value_try":40,"value_usd":1}]')`, timestamp, index+1)
		if err != nil {
			t.Fatal(err)
		}
	}

	snapshots := NewSnapshotService(service.db, nil, nil)

	page, err := snapshots.GetSnapshots(t.Context(), 10, 0, 1)
	if err != nil || len(page) != 1 || page[0].TotalValueTRY != 2 {
		t.Fatalf("page = %+v, %v", page, err)
	}

	chart, err := snapshots.GetSnapshotsForChart(t.Context(), 1)
	if err != nil || len(chart) != 1 || chart[0].ValueTRY != 2 {
		t.Fatalf("chart = %+v, %v", chart, err)
	}

	history, err := snapshots.GetHoldingHistory(t.Context(), 1, 1)
	if err != nil || len(history) != 1 {
		t.Fatalf("history = %+v, %v", history, err)
	}
}

func TestRestoreRejectsInvalidSequenceAtomically(t *testing.T) {
	t.Parallel()
	source := openTestDB(t, "source.db")
	target := openTestDB(t, "target.db")

	_, err := source.db.ExecContext(
		t.Context(),
		`INSERT INTO holdings (name,asset_type) VALUES ('Backup','Cash');
UPDATE sqlite_sequence SET seq = 'bad' WHERE name = 'holdings'`,
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = target.db.ExecContext(
		t.Context(),
		`INSERT INTO holdings (name,asset_type) VALUES ('Live','Cash')`,
	)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "backup.db")

	err = source.Backup(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	err = target.RestoreFrom(t.Context(), path)
	if !errors.Is(err, ErrIncompatibleBackup) {
		t.Fatalf("RestoreFrom = %v", err)
	}

	if countRows(t, target, "SELECT COUNT(*) FROM holdings WHERE name = 'Live'") != 1 {
		t.Fatal("invalid sequence changed live data")
	}
}

func TestHistoryLegacyGoTimestampsAndStableOrdering(t *testing.T) {
	t.Parallel()
	service := openTestDB(t, "legacy.db")

	now := time.Now().UTC().Truncate(time.Second)
	for index, instant := range []time.Time{now.Add(-time.Hour), now, now} {
		timestamp := instant.In(time.FixedZone("EEST", 3*60*60)).String()

		_, err := service.db.ExecContext(t.Context(), `INSERT INTO snapshots
(timestamp,total_value_try,total_value_usd,usd_try_rate,holdings_json) VALUES (?, ?, 1,40,'[]')`, timestamp, index+1)
		if err != nil {
			t.Fatal(err)
		}
	}

	snapshots := NewSnapshotService(service.db, nil, nil)

	chart, err := snapshots.GetSnapshotsForChart(t.Context(), 1)
	if err != nil || len(chart) != 3 {
		t.Fatalf("chart = %+v, %v", chart, err)
	}

	for index, point := range chart {
		if point.ValueTRY != float64(index+1) {
			t.Fatalf("unstable order: %+v", chart)
		}
	}

	page, err := snapshots.GetSnapshots(t.Context(), 1, 1, 1)
	if err != nil || len(page) != 1 || page[0].TotalValueTRY != 2 {
		t.Fatalf("page = %+v, %v", page, err)
	}
}
