package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

const restoreCurrencyTRY, restoreCurrencyUSD = "TRY", "USD"

// Validate one row at a time so backup size does not determine memory use.
// COALESCE preserves nullable fields supported by older schema writers.
func validateRestoreData(ctx context.Context, conn sqlx.QueryerContext) error {
	checks := []struct {
		validate func(*sqlx.Rows) error
		table    string
		columns  string
	}{
		{table: "holdings", columns: `id, name, asset_type, quantity, base_currency, manual_price,
manual_price_at, created_at, updated_at, COALESCE(est_price, 0) AS est_price,
COALESCE(ticker, '') AS ticker, COALESCE(price_provider, '') AS price_provider,
COALESCE(instrument_id, '') AS instrument_id, COALESCE(basket, '') AS basket,
COALESCE(location, '') AS location, COALESCE(notes, '') AS notes`, validate: validateRestoreHolding},
		{table: "snapshots", columns: `id, timestamp, created_at, holdings_json, total_value_try,
total_value_usd, usd_try_rate, stale_count, failed_count`, validate: validateRestoreSnapshot},
		{
			table: "price_cache",
			columns: `id, provider, instrument_id, price, currency, source, fetched_at,
expires_at, error_count, COALESCE(last_error, '') AS last_error`,
			validate: validateRestorePrice,
		},
		{table: "settings", columns: `key, value, updated_at`, validate: validateRestoreSetting},
	}
	for _, check := range checks {
		err := validateRestoreRows(ctx, conn, check.columns, check.table, check.validate)
		if err != nil {
			return err
		}
	}

	return nil
}

func validateRestoreRows(ctx context.Context, conn sqlx.QueryerContext, columns, table string,
	validate func(*sqlx.Rows) error,
) error {
	rows, err := conn.QueryxContext(ctx, "SELECT "+columns+" FROM "+restoreSchema+"."+table)
	if err != nil {
		return fmt.Errorf("%w: read %s: %w", ErrIncompatibleBackup, table, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		err = validate(rows)
		if err != nil {
			return fmt.Errorf("%w: invalid %s: %w", ErrIncompatibleBackup, table, err)
		}
	}

	err = rows.Err()
	if err != nil {
		return fmt.Errorf("%w: read %s: %w", ErrIncompatibleBackup, table, err)
	}

	return nil
}

func restoreNonnegative(values ...float64) bool {
	for _, value := range values {
		if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}

	return true
}

func validateRestoreHolding(rows *sqlx.Rows) error {
	var holding models.Holding

	err := rows.StructScan(&holding)
	if err != nil {
		return fmt.Errorf("decode holding: %w", err)
	}

	assetTypes := []models.AssetType{
		models.AssetTypeStock, models.AssetTypeETF,
		models.AssetTypeTRFund, models.AssetTypeCrypto, models.AssetTypeGold, models.AssetTypeCash,
	}
	if holding.Name == "" || !slices.Contains(assetTypes, holding.AssetType) ||
		!slices.Contains([]string{restoreCurrencyTRY, restoreCurrencyUSD}, holding.BaseCurrency) ||
		!restoreNonnegative(holding.Quantity, holding.EstPrice) {
		return fmt.Errorf("%w: holding values", ErrIncompatibleBackup)
	}

	if holding.ManualPrice.Valid && !restoreNonnegative(holding.ManualPrice.Float64) {
		return fmt.Errorf("%w: manual price", ErrIncompatibleBackup)
	}

	return nil
}

func validateRestorePrice(rows *sqlx.Rows) error {
	var entry models.PriceCacheEntry

	err := rows.StructScan(&entry)
	if err != nil {
		return fmt.Errorf("decode cached price: %w", err)
	}

	if !restoreNonnegative(entry.Price) || entry.ErrorCount < 0 {
		return fmt.Errorf("%w: cached price values", ErrIncompatibleBackup)
	}

	return nil
}

func validateRestoreSnapshot(rows *sqlx.Rows) error {
	var snapshot models.Snapshot

	err := rows.StructScan(&snapshot)
	if err != nil {
		return fmt.Errorf("decode snapshot: %w", err)
	}

	if !restoreNonnegative(snapshot.TotalValueTRY, snapshot.TotalValueUSD, snapshot.USDTRYRate) ||
		snapshot.USDTRYRate == 0 || snapshot.StaleCount < 0 || snapshot.FailedCount < 0 {
		return fmt.Errorf("%w: snapshot values", ErrIncompatibleBackup)
	}

	return validateRestoreSnapshotJSON(snapshot.HoldingsJSON)
}

func validateRestoreSnapshotJSON(raw string) error {
	decoder := json.NewDecoder(strings.NewReader(raw))

	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode snapshot holdings: %w", err)
	}
	// Older empty snapshots may encode a nil slice as null.
	if token == nil {
		return restoreJSONEnd(decoder)
	}

	if token != json.Delim('[') {
		return fmt.Errorf("%w: snapshot holdings must be an array", ErrIncompatibleBackup)
	}

	for decoder.More() {
		var holding struct {
			Name               string  `json:"name"`
			Currency           string  `json:"currency"`
			ID                 int     `json:"id"`
			Quantity           float64 `json:"quantity"`
			Price              float64 `json:"price"`
			ValueTRY           float64 `json:"value_try"`
			ValueUSD           float64 `json:"value_usd"`
			IsStale            bool    `json:"is_stale"`
			IsFailed           bool    `json:"is_failed"`
			ValuationAvailable bool    `json:"valuation_available"`
		}

		err = decoder.Decode(&holding)
		if err != nil {
			return fmt.Errorf("decode snapshot holding: %w", err)
		}

		if !restoreNonnegative(
			holding.Quantity,
			holding.Price,
			holding.ValueTRY,
			holding.ValueUSD,
		) {
			return fmt.Errorf("%w: snapshot holding values", ErrIncompatibleBackup)
		}
	}

	_, err = decoder.Token()
	if err != nil {
		return fmt.Errorf("decode snapshot array: %w", err)
	}

	return restoreJSONEnd(decoder)
}

func restoreJSONEnd(decoder *json.Decoder) error {
	_, err := decoder.Token()
	if !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing snapshot data", ErrIncompatibleBackup)
	}

	return nil
}

func validateRestoreSetting(rows *sqlx.Rows) error {
	var key, value string

	var updatedAt time.Time

	err := rows.Scan(&key, &value, &updatedAt)
	if err != nil {
		return fmt.Errorf("decode setting: %w", err)
	}
	// Missing or empty settings use application defaults; unknown keys are kept.
	if value == "" {
		return nil
	}

	valid := true

	switch key {
	case "display_currency":
		valid = slices.Contains([]string{restoreCurrencyTRY, restoreCurrencyUSD}, value)
	case "dark_mode":
		valid = slices.Contains([]string{"system", "light", "dark"}, value)
	case "refresh_interval":
		const minRefresh, maxRefresh = 10, 3600

		interval, err := strconv.Atoi(value)
		valid = err == nil && interval >= minRefresh && interval <= maxRefresh
	default:
	}

	if !valid {
		return fmt.Errorf("%w: setting %s", ErrIncompatibleBackup, key)
	}

	return nil
}
