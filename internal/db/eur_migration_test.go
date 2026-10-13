package db

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jmoiron/sqlx"
)

func TestEURMigrationPreservesHoldingsAndSequences(t *testing.T) {
	t.Parallel()

	for _, keepHolding := range []bool{false, true} {
		t.Run(strconv.FormatBool(keepHolding), func(t *testing.T) {
			t.Parallel()
			database := openPreEURMigrationDB(t)

			_, err := database.ExecContext(t.Context(), `INSERT INTO holdings
(id, name, ticker, asset_type, quantity, price_provider, instrument_id, base_currency,
basket, location, notes, est_price, manual_price, manual_price_at, created_at, updated_at)
VALUES (7, 'Original', 'TEST', 'Stock', 2.5, 'Manual', 'TEST', 'USD', 'Long term',
'Broker', 'Keep this', 10, NULL, '2020-01-01 00:00:00', '2020-01-02 00:00:00', '2020-01-03 00:00:00');
INSERT INTO holdings (id, name, asset_type) VALUES (9007199254740993, 'Deleted', 'Cash');
DELETE FROM holdings WHERE id = 9007199254740993;
INSERT INTO snapshots (id, total_value_try, total_value_usd, usd_try_rate, holdings_json)
VALUES (11, 100, 5, 20, '[{"id":7,"value_try":100,"value_usd":5}]');
INSERT INTO snapshots (id, total_value_try, total_value_usd, usd_try_rate, holdings_json)
VALUES (33, 100, 5, 20, '[]');
DELETE FROM snapshots WHERE id = 33;
INSERT INTO price_cache (id, provider, instrument_id, price, currency, source, expires_at)
VALUES (99, 'Manual', 'TEST', 10, 'USD', 'fixture', CURRENT_TIMESTAMP);
DELETE FROM price_cache WHERE id = 99`)
			if err != nil {
				t.Fatal(err)
			}

			if !keepHolding {
				_, err = database.ExecContext(t.Context(), "DELETE FROM holdings")
				if err != nil {
					t.Fatal(err)
				}
			}

			err = migrate(database)
			if err != nil {
				t.Fatal(err)
			}

			if keepHolding {
				assertEURMigrationCount(t, database, `SELECT COUNT(*) FROM holdings WHERE id = 7
AND name = 'Original' AND ticker = 'TEST' AND asset_type = 'Stock' AND quantity = 2.5
AND price_provider = 'Manual' AND instrument_id = 'TEST' AND base_currency = 'USD'
AND basket = 'Long term' AND location = 'Broker' AND notes = 'Keep this' AND est_price = 10
AND manual_price IS NULL AND manual_price_at = '2020-01-01 00:00:00'
AND created_at = '2020-01-02 00:00:00' AND updated_at = '2020-01-03 00:00:00'`, 1)
			}

			assertEURMigrationCount(t, database, `SELECT COUNT(*) FROM snapshots WHERE id = 11
AND total_value_try = 100 AND total_value_usd = 5 AND usd_try_rate = 20
AND total_value_eur IS NULL AND eur_try_rate IS NULL
AND holdings_json = '[{"id":7,"value_try":100,"value_usd":5}]'`, 1)
			assertEURMigrationCount(t, database, `SELECT COUNT(*) FROM sqlite_schema
WHERE type = 'index' AND name IN ('idx_holdings_asset_type', 'idx_holdings_basket',
'idx_holdings_location')`, 3)

			assertEURMigrationSequences(t, database)

			result, err := database.ExecContext(t.Context(), `INSERT INTO holdings
(name, asset_type, base_currency) VALUES ('Euro cash', 'Cash', 'EUR')`)
			if err != nil {
				t.Fatal(err)
			}

			identity, err := result.LastInsertId()
			if err != nil || identity != 9007199254740994 {
				t.Fatalf("new EUR holding ID = %d, %v", identity, err)
			}

			_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
(name, asset_type, base_currency) VALUES ('Unsupported cash', 'Cash', 'GBP')`)
			if err == nil {
				t.Fatal("currency constraint was not preserved")
			}
		})
	}
}

func assertEURMigrationSequences(t *testing.T, database *sqlx.DB) {
	t.Helper()

	for table, want := range map[string]int64{
		"holdings": 9007199254740993, "snapshots": 33, "price_cache": 99,
	} {
		var sequence int64

		err := database.GetContext(t.Context(), &sequence,
			"SELECT seq FROM sqlite_sequence WHERE name = ?", table)
		if err != nil || sequence != want {
			t.Fatalf("%s sequence = %d, %v; want %d", table, sequence, err, want)
		}
	}
}

func TestEURMigrationRecoversSequenceFromExistingHoldings(t *testing.T) {
	t.Parallel()
	database := openPreEURMigrationDB(t)

	_, err := database.ExecContext(t.Context(), `INSERT INTO holdings (id, name, asset_type)
VALUES (75, 'Original', 'Cash'); DELETE FROM sqlite_sequence WHERE name = 'holdings'`)
	if err != nil {
		t.Fatal(err)
	}

	err = migrate(database)
	if err != nil {
		t.Fatal(err)
	}

	assertEURMigrationCount(
		t,
		database,
		"SELECT seq FROM sqlite_sequence WHERE name = 'holdings'",
		75,
	)
}

func openPreEURMigrationDB(t *testing.T) *sqlx.DB {
	t.Helper()

	database, err := sqlx.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })
	database.SetMaxOpenConns(1)

	const previousVersion = 11
	for _, migration := range Migrations[:previousVersion] {
		_, err = database.ExecContext(t.Context(), migration)
		if err != nil {
			t.Fatal(err)
		}
	}

	_, err = database.ExecContext(t.Context(), "PRAGMA user_version = 11")
	if err != nil {
		t.Fatal(err)
	}

	return database
}

func assertEURMigrationCount(t *testing.T, database *sqlx.DB, query string, want int) {
	t.Helper()

	var count int

	err := database.GetContext(t.Context(), &count, query)
	if err != nil || count != want {
		t.Fatalf("query result = %d, %v; want %d: %s", count, err, want, query)
	}
}
