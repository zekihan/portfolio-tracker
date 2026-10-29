package services

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRestoreCommaFractionTimestampsRemainVisible(t *testing.T) {
	t.Parallel()

	for name, timestamp := range map[string]string{
		"ISO":       "2099-01-02T12:34:56,123456789+03:00",
		"legacy Go": "2099-01-02 12:34:56,123456789 +0300 TRT",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := openTestDB(t, "source.db")
			target := openTestDB(t, "target.db")
			seedRestoreValidationData(t, source, "Imported")

			_, err := source.db.ExecContext(
				t.Context(),
				"UPDATE snapshots SET timestamp = ?",
				timestamp,
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

			// Restore accepts these Go-readable fractional seconds. Keep the original
			// text and nanoseconds while making the row visible in every history query.
			var stored string

			err = target.db.GetContext(
				t.Context(),
				&stored,
				"SELECT CAST(timestamp AS TEXT) FROM snapshots",
			)
			if err != nil || stored != timestamp {
				t.Fatalf("stored timestamp = %q, %v; want %q", stored, err, timestamp)
			}

			assertRestoredTimestampHistory(t, target)
		})
	}
}

func assertRestoredTimestampHistory(t *testing.T, target *SettingsService) {
	t.Helper()

	service := NewSnapshotService(target.db, nil, nil)
	want := time.Date(2099, time.January, 2, 9, 34, 56, 123456789, time.UTC)

	for _, days := range []int{0, 1} {
		snapshots, queryErr := service.GetSnapshots(t.Context(), 10, 0, days)
		if queryErr != nil || len(snapshots) != 1 {
			t.Errorf(
				"GetSnapshots(days=%d) = %d rows, %v; want 1",
				days,
				len(snapshots),
				queryErr,
			)
		} else if !snapshots[0].Timestamp.Equal(want) {
			t.Errorf("timestamp = %v; want %v", snapshots[0].Timestamp, want)
		}
	}

	chart, err := service.GetSnapshotsForChart(t.Context(), 1)
	if err != nil || len(chart) != 1 {
		t.Errorf("chart = %d points, %v; want 1", len(chart), err)
	}

	history, err := service.GetHoldingHistory(t.Context(), 1, 1)
	if err != nil || len(history) != 1 {
		t.Errorf("holding history = %d points, %v; want 1", len(history), err)
	}
}
