package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
)

const SchemaVersion = 2

var Migrations = []string{
	// Schema v1 - Initial schema
	`
	CREATE TABLE IF NOT EXISTS holdings (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		ticker TEXT,
		asset_type TEXT NOT NULL CHECK (asset_type IN ('Stock', 'ETF', 'TR Fund', 'Crypto', 'Gold', 'Cash')),
		quantity REAL NOT NULL DEFAULT 0,
		price_provider TEXT,
		instrument_id TEXT,
		base_currency TEXT NOT NULL DEFAULT 'TRY' CHECK (base_currency IN ('TRY', 'USD')),
		basket TEXT,
		location TEXT,
		notes TEXT,
		est_price REAL,
		manual_price REAL DEFAULT 0,
		manual_price_at DATETIME,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_holdings_asset_type ON holdings(asset_type);
	CREATE INDEX IF NOT EXISTS idx_holdings_basket ON holdings(basket);
	CREATE INDEX IF NOT EXISTS idx_holdings_location ON holdings(location);
	`,

	// Schema v2 - Fix NULL manual_price values
		`
		UPDATE holdings SET manual_price = 0 WHERE manual_price IS NULL;
		`,

		// Schema v3 - Price cache table
	`
	CREATE TABLE IF NOT EXISTS price_cache (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		provider TEXT NOT NULL,
		instrument_id TEXT NOT NULL,
		price REAL NOT NULL,
		currency TEXT NOT NULL,
		source TEXT NOT NULL,
		fetched_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		expires_at DATETIME NOT NULL,
		error_count INTEGER NOT NULL DEFAULT 0,
		last_error TEXT,
		UNIQUE(provider, instrument_id)
	);

	CREATE INDEX IF NOT EXISTS idx_price_cache_expires ON price_cache(expires_at);
	`,

	// Schema v3 - Snapshots table
	`
	CREATE TABLE IF NOT EXISTS snapshots (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		total_value_try REAL NOT NULL,
		total_value_usd REAL NOT NULL,
		usd_try_rate REAL NOT NULL,
		stale_count INTEGER NOT NULL DEFAULT 0,
		failed_count INTEGER NOT NULL DEFAULT 0,
		holdings_json TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_snapshots_timestamp ON snapshots(timestamp);
	`,

	// Schema v4 - Settings table
	`
	CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	INSERT OR IGNORE INTO settings (key, value) VALUES ('display_currency', 'TRY');
	INSERT OR IGNORE INTO settings (key, value) VALUES ('dark_mode', 'system');
	INSERT OR IGNORE INTO settings (key, value) VALUES ('refresh_interval', '60');
	`,

	// Schema v5 - Provider registry table
	`
	CREATE TABLE IF NOT EXISTS provider_registry (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		type TEXT NOT NULL CHECK (type IN ('Stock/ETF', 'TR Fund', 'Gold', 'Crypto', 'FX', 'Manual')),
		key_required BOOLEAN NOT NULL DEFAULT 0,
		key_env_var TEXT,
		base_url TEXT,
		ttl_seconds INTEGER NOT NULL DEFAULT 60,
		enabled BOOLEAN NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	INSERT OR IGNORE INTO provider_registry (name, type, key_required, key_env_var, base_url, ttl_seconds, enabled) VALUES
		('Yahoo Finance', 'Stock/ETF', 0, '', 'https://query1.finance.yahoo.com', 60, 1),
		('Alpha Vantage', 'Stock/ETF', 1, 'ALPHA_VANTAGE_KEY', 'https://www.alphavantage.co', 60, 1),
		('Twelve Data', 'Stock/ETF', 1, 'TWELVE_DATA_KEY', 'https://api.twelvedata.com', 60, 1),
		('Finnhub', 'Stock/ETF', 1, 'FINNHUB_KEY', 'https://finnhub.io', 60, 1),
		('Pusula Portföy', 'TR Fund', 0, '', 'https://www.pusulaportfoy.com.tr', 60, 1),
		('Garanti BBVA', 'TR Fund', 0, '', 'https://www.garantibbva.com.tr', 60, 1),
		('İZKO Gold', 'Gold', 0, '', 'https://www.izko.com.tr', 60, 1),
		('TradingView BIST', 'Gold', 0, '', 'https://symbol-search.tradingview.com', 60, 1),
		('TradingView Crypto', 'Crypto', 0, '', 'https://symbol-search.tradingview.com', 60, 1),
		('CoinGecko', 'Crypto', 1, 'COINGECKO_KEY', 'https://api.coingecko.com', 60, 1),
		('Frankfurter', 'FX', 0, '', 'https://api.frankfurter.dev', 60, 1),
		('Manual', 'Manual', 0, '', '', 0, 1);
	`,
}

func Open(dbPath string) (*sqlx.DB, error) {
	db, err := sqlx.Open("sqlite3", dbPath+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Hour)

	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return db, nil
}

func migrate(db *sqlx.DB) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var version int
	err = tx.Get(&version, "PRAGMA user_version")
	if err != nil && !strings.Contains(err.Error(), "no such table") {
		return err
	}

	for i := version; i < len(Migrations); i++ {
		if _, err := tx.Exec(Migrations[i]); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec("PRAGMA user_version = " + fmt.Sprintf("%d", i+1)); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func GetSetting(db *sqlx.DB, key string) (string, error) {
	var value string
	err := db.Get(&value, "SELECT value FROM settings WHERE key = ?", key)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func SetSetting(db *sqlx.DB, key, value string) error {
	_, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`, key, value)
	return err
}