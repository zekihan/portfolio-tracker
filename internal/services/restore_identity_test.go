package services

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

type restoreFixedFX struct{}

func (restoreFixedFX) Name() string       { return "Frankfurter" }
func (restoreFixedFX) Type() string       { return "FX" }
func (restoreFixedFX) TTL() time.Duration { return time.Hour }
func (restoreFixedFX) GetPrice(context.Context, string) (models.Price, error) {
	return models.Price{Value: 40, Currency: "TRY", Source: "fixture", FetchedAt: time.Now()}, nil
}

func TestRestoreRecoversIdentitiesAfterLegacyRestoreChain(t *testing.T) {
	t.Parallel()

	for _, version := range []int{9, 10} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			t.Parallel()
			original := openTestDB(t, "original.db")
			legacy := openTestDB(t, "legacy-restored.db")
			target := openTestDB(t, "current.db")
			registry := price.NewRegistry()
			registry.Register(restoreFixedFX{})
			prices := price.NewService(original.db, registry)

			t.Cleanup(func() { _ = prices.Close() })

			holdings := NewHoldingService(original.db, prices)
			deleted := models.Holding{
				Name:         "Deleted cash",
				AssetType:    models.AssetTypeCash,
				BaseCurrency: "TRY",
				Quantity:     1,
				ManualPrice:  sql.NullFloat64{Float64: 100, Valid: true},
			}

			err := holdings.Create(t.Context(), &deleted)
			if err != nil {
				t.Fatal(err)
			}

			err = NewSnapshotService(original.db, prices, holdings).CreateSnapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			err = holdings.Delete(t.Context(), deleted.ID)
			if err != nil {
				t.Fatal(err)
			}

			execRestoreFixture(t, original, "PRAGMA user_version = "+strconv.Itoa(version))
			firstPath := filepath.Join(t.TempDir(), "original-backup.db")

			err = original.Backup(t.Context(), firstPath)
			if err != nil {
				t.Fatal(err)
			}

			copyLegacyRestoreFixture(t, legacy, firstPath)

			if got := countRows(
				t,
				legacy,
				"SELECT COALESCE(MAX(seq), 0) FROM sqlite_sequence WHERE name = 'holdings'",
			); got >= deleted.ID {
				t.Fatalf("legacy fixture retained the deleted holding's sequence: %d", got)
			}

			execRestoreFixture(t, legacy, "PRAGMA user_version = "+strconv.Itoa(version))
			secondPath := filepath.Join(t.TempDir(), "backup-after-legacy-restore.db")

			err = legacy.Backup(t.Context(), secondPath)
			if err != nil {
				t.Fatal(err)
			}

			err = target.RestoreFrom(t.Context(), secondPath)
			if err != nil {
				t.Fatal(err)
			}

			created := models.Holding{
				Name:         "Unrelated cash",
				AssetType:    models.AssetTypeCash,
				BaseCurrency: "TRY",
				Quantity:     2,
				ManualPrice:  sql.NullFloat64{Float64: 200, Valid: true},
			}

			err = NewHoldingService(target.db, nil).Create(t.Context(), &created)
			if err != nil {
				t.Fatal(err)
			}

			assertRestoredHistoryIdentity(t, target, created.ID, deleted.ID)
		})
	}
}

func assertRestoredHistoryIdentity(
	t *testing.T,
	target *SettingsService,
	createdID, deletedID int,
) {
	t.Helper()

	if createdID <= deletedID {
		t.Fatalf("new holding %d reused historical identity %d", createdID, deletedID)
	}

	history := NewSnapshotService(target.db, nil, nil)

	points, err := history.GetHoldingHistory(t.Context(), createdID, 1)
	if err != nil || len(points) != 0 {
		t.Fatalf("new holding inherited history: %+v, %v", points, err)
	}

	points, err = history.GetHoldingHistory(t.Context(), deletedID, 1)
	if err != nil || len(points) != 1 || points[0].ValueTRY != 100 {
		t.Fatalf("deleted holding history changed: %+v, %v", points, err)
	}
}

// Reproduce the old restore's user-table copy without preserving sequence rows.
// Nothing edits sqlite_sequence: losing it is a natural result of the old flow.
func copyLegacyRestoreFixture(t *testing.T, service *SettingsService, path string) {
	t.Helper()

	conn, err := service.db.Connx(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	_, err = conn.ExecContext(t.Context(), "ATTACH DATABASE ? AS legacy_src", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.ExecContext(t.Context(), "DETACH DATABASE legacy_src") }()

	tx, err := conn.BeginTxx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, table := range restoreTables {
		columns, columnErr := tableColumns(t.Context(), tx, "main", table)
		if columnErr != nil {
			t.Fatal(columnErr)
		}

		for index := range columns {
			columns[index] = quoteIdentifier(columns[index])
		}

		list := strings.Join(columns, ", ")

		_, err = tx.ExecContext(t.Context(), "DELETE FROM main."+table+
			"; INSERT INTO main."+table+" ("+list+") SELECT "+list+" FROM legacy_src."+table)
		if err != nil {
			t.Fatal(err)
		}
	}

	err = tx.Commit()
	if err != nil {
		t.Fatal(err)
	}
}

func TestRestoreUsesHighestStoredAndHistoricalIdentity(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
		live   string
		wantID int64
	}{
		{name: "all snapshots and exact integers", source: `DELETE FROM holdings;
DELETE FROM sqlite_sequence WHERE name = 'holdings';
UPDATE snapshots SET holdings_json = '[{"id":9007199254740993},{"id":2}]';
INSERT INTO snapshots (total_value_try, total_value_usd, usd_try_rate, holdings_json)
VALUES (1, 1, 1, '[{"id":3}]')`, wantID: 9007199254740994},
		{name: "case alias cannot conceal identity", source: `DELETE FROM holdings;
DELETE FROM sqlite_sequence WHERE name = 'holdings';
UPDATE snapshots SET holdings_json = '[{"id":55,"ID":1}]'`, wantID: 56},
		{
			name:   "source sequence",
			source: `UPDATE sqlite_sequence SET seq = 50 WHERE name = 'holdings'`,
			wantID: 51,
		},
		{name: "source rows", source: `UPDATE holdings SET id = 75;
UPDATE sqlite_sequence SET seq = 1 WHERE name = 'holdings'`, wantID: 76},
		{name: "live sequence", live: `UPDATE sqlite_sequence SET seq = 100 WHERE name = 'holdings';
DELETE FROM holdings`, wantID: 101},
		{name: "live historical IDs", live: `DELETE FROM holdings;
DELETE FROM sqlite_sequence WHERE name = 'holdings';
UPDATE snapshots SET holdings_json = '[{"id":150}]'`, wantID: 151},
		{name: "damaged live history", live: `UPDATE snapshots SET holdings_json =
'[null,"bad",{"id":9223372036854775807},{"id":175},'`, wantID: 176},
		{
			name:   "unreadable live history",
			live:   `UPDATE snapshots SET holdings_json = '{'`,
			wantID: 2,
		},
		{name: "live rows", live: `UPDATE holdings SET id = 125;
UPDATE sqlite_sequence SET seq = 1 WHERE name = 'holdings'`, wantID: 126},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := openTestDB(t, "source.db")
			target := openTestDB(t, "target.db")
			seedRestoreValidationData(t, source, "Backup")
			seedRestoreValidationData(t, target, "Live")

			if test.source != "" {
				execRestoreFixture(t, source, test.source)
			}

			if test.live != "" {
				execRestoreFixture(t, target, test.live)
			}

			path := filepath.Join(t.TempDir(), "backup.db")

			err := source.Backup(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}

			err = target.RestoreFrom(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}

			result, err := target.db.ExecContext(t.Context(), `INSERT INTO holdings
(name, asset_type) VALUES ('New holding', 'Cash')`)
			if err != nil {
				t.Fatal(err)
			}

			got, err := result.LastInsertId()
			if err != nil || got != test.wantID {
				t.Fatalf("new holding ID = %d, %v; want %d", got, err, test.wantID)
			}
		})
	}
}

func TestRestoreRepairsPoisonedLiveSequences(t *testing.T) {
	t.Parallel()

	for _, poison := range []string{"9223372036854775807", "NULL", "'invalid'", "-1", "1e999"} {
		t.Run(poison, func(t *testing.T) {
			t.Parallel()
			source := openTestDB(t, "source.db")
			target := openTestDB(t, "target.db")
			seedRestoreValidationData(t, source, "Backup")
			seedRestoreValidationData(t, target, "Live")
			// Keep valid duplicate maxima while replacing the poisoned counter.
			execRestoreFixture(t, target, `UPDATE sqlite_sequence SET seq = `+poison+`
WHERE name IN ('holdings', 'snapshots', 'price_cache');
INSERT INTO sqlite_sequence (name, seq) VALUES ('holdings', 50);
UPDATE snapshots SET id = 9223372036854775807;
UPDATE price_cache SET id = 75`)
			path := filepath.Join(t.TempDir(), "backup.db")

			err := source.Backup(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}

			err = target.RestoreFrom(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}

			for _, test := range []struct {
				table string
				query string
				want  int64
			}{
				{
					table: "holdings", want: 51,
					query: `INSERT INTO holdings (name, asset_type) VALUES ('New', 'Cash')`,
				},
				{
					table: "snapshots", want: 2,
					query: `INSERT INTO snapshots (total_value_try, total_value_usd, usd_try_rate, holdings_json)
VALUES (1, 1, 1, '[]')`,
				},
				{
					table: "price_cache", want: 76,
					query: `INSERT INTO price_cache (provider, instrument_id, price, currency, source, expires_at)
VALUES ('Manual', 'NEW', 1, 'TRY', 'fixture', CURRENT_TIMESTAMP)`,
				},
			} {
				result, insertErr := target.db.ExecContext(t.Context(), test.query)
				if insertErr != nil {
					t.Fatal(insertErr)
				}

				got, idErr := result.LastInsertId()
				if idErr != nil || got != test.want {
					t.Fatalf("%s new ID = %d, %v; want %d", test.table, got, idErr, test.want)
				}

				if rows := countRows(
					t,
					target,
					"SELECT COUNT(*) FROM sqlite_sequence WHERE name = '"+test.table+"'",
				); rows != 1 {
					t.Fatalf("%s retained %d sequence rows", test.table, rows)
				}
			}
		})
	}
}
