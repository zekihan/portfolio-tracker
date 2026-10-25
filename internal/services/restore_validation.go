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

const restoreCurrencyTRY, restoreCurrencyUSD, restoreCurrencyEUR = "TRY", "USD", "EUR"

// Validate one row at a time so backup size does not determine memory use.
// COALESCE preserves nullable fields supported by older schema writers.
func validateRestoreData(
	ctx context.Context,
	conn sqlx.QueryerContext,
	sourceVersion int,
) (int64, error) {
	var historicalID int64

	eurColumns := "total_value_eur, eur_try_rate"
	if sourceVersion < restoreEURVersion {
		eurColumns = "NULL AS total_value_eur, NULL AS eur_try_rate"
	}

	validateSnapshot := func(rows *sqlx.Rows) error {
		id, err := validateRestoreSnapshot(ctx, rows)
		historicalID = max(historicalID, id)

		return err
	}

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
		{
			table: restoreSnapshotsTable,
			columns: `id, timestamp, created_at, holdings_json, total_value_try,
total_value_usd, usd_try_rate, stale_count, failed_count, ` + eurColumns,
			validate: validateSnapshot,
		},
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
			return 0, err
		}
	}

	return historicalID, nil
}

func validateRestoreRows(ctx context.Context, conn sqlx.QueryerContext, columns, table string,
	validate func(*sqlx.Rows) error,
) error {
	rows, err := conn.QueryxContext(
		ctx,
		"SELECT "+columns+" FROM "+restoreSchema+"."+table+" NOT INDEXED",
	)
	if err != nil {
		return fmt.Errorf("%w: read %s: %w", ErrIncompatibleBackup, table, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		cancelErr := ctx.Err()
		if cancelErr != nil {
			return fmt.Errorf("validate backup: %w", cancelErr)
		}

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
	if !restoreValidID(int64(holding.ID)) || holding.Name == "" ||
		!slices.Contains(assetTypes, holding.AssetType) ||
		!slices.Contains(
			[]string{restoreCurrencyTRY, restoreCurrencyUSD, restoreCurrencyEUR},
			holding.BaseCurrency,
		) ||
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

	if !restoreValidID(int64(entry.ID)) || !restoreNonnegative(entry.Price) ||
		entry.ErrorCount < 0 {
		return fmt.Errorf("%w: cached price values", ErrIncompatibleBackup)
	}

	return nil
}

func validateRestoreSnapshot(ctx context.Context, rows *sqlx.Rows) (int64, error) {
	var snapshot models.Snapshot

	err := rows.StructScan(&snapshot)
	if err != nil {
		return 0, fmt.Errorf("decode snapshot: %w", err)
	}

	if !restoreValidID(int64(snapshot.ID)) ||
		!restoreNonnegative(snapshot.TotalValueTRY, snapshot.TotalValueUSD, snapshot.USDTRYRate) ||
		snapshot.USDTRYRate == 0 || snapshot.StaleCount < 0 ||
		snapshot.FailedCount < 0 {
		return 0, fmt.Errorf("%w: snapshot values", ErrIncompatibleBackup)
	}

	if (snapshot.TotalValueEUR == nil) != (snapshot.EURTRYRate == nil) {
		return 0, fmt.Errorf("%w: incomplete EUR snapshot values", ErrIncompatibleBackup)
	}

	if snapshot.TotalValueEUR != nil &&
		(!restoreNonnegative(*snapshot.TotalValueEUR, *snapshot.EURTRYRate) || *snapshot.EURTRYRate == 0) {
		return 0, fmt.Errorf("%w: EUR snapshot values", ErrIncompatibleBackup)
	}

	return validateRestoreSnapshotJSON(ctx, snapshot.HoldingsJSON)
}

func restoreValidID(id int64) bool {
	return id > 0 && id < math.MaxInt64
}

func validateRestoreSnapshotJSON(ctx context.Context, raw string) (int64, error) {
	var historicalID int64

	err := preflightRestoreJSON(ctx, raw)
	if err != nil {
		return 0, err
	}

	decoder := json.NewDecoder(strings.NewReader(raw))

	token, err := decoder.Token()
	if err != nil {
		return 0, fmt.Errorf("decode snapshot holdings: %w", err)
	}
	// Older empty snapshots may encode a nil slice as null.
	if token == nil {
		return historicalID, restoreJSONEnd(decoder)
	}

	if token != json.Delim('[') {
		return 0, fmt.Errorf("%w: snapshot holdings must be an array", ErrIncompatibleBackup)
	}

	for decoder.More() {
		cancelErr := ctx.Err()
		if cancelErr != nil {
			return 0, fmt.Errorf("decode snapshot holdings: %w", cancelErr)
		}

		id, decodeErr := decodeRestoreSnapshotHolding(decoder)
		if decodeErr != nil {
			return 0, decodeErr
		}

		historicalID = max(historicalID, id)
	}

	_, err = decoder.Token()
	if err != nil {
		return 0, fmt.Errorf("decode snapshot array: %w", err)
	}

	return historicalID, restoreJSONEnd(decoder)
}

// Match the history reader's exact JSON keys. Case-insensitive struct decoding
// could otherwise let an alias hide a historical ID or an invalid value.
func decodeRestoreSnapshotHolding(decoder *json.Decoder) (int64, error) {
	holding, err := decodeRestoreFields(decoder)
	if err != nil {
		return 0, err
	}

	var id int64

	err = json.Unmarshal(holding["id"], &id)
	if err != nil || !restoreValidID(id) {
		return 0, fmt.Errorf("%w: snapshot holding identity", ErrIncompatibleBackup)
	}

	var quantity, price, valueTRY, valueUSD float64

	var valueEUR *float64

	var name, currency string

	var stale, failed, available bool

	fields := []struct {
		value any
		key   string
	}{
		{key: "quantity", value: &quantity},
		{key: "price", value: &price},
		{key: "value_try", value: &valueTRY},
		{key: "value_usd", value: &valueUSD},
		{key: "value_eur", value: &valueEUR},
		{key: "name", value: &name},
		{key: "currency", value: &currency},
		{key: "is_stale", value: &stale},
		{key: "is_failed", value: &failed},
		{key: "valuation_available", value: &available},
	}
	for _, field := range fields {
		raw, exists := holding[field.key]
		if !exists {
			continue
		}

		err = json.Unmarshal(raw, field.value)
		if err != nil {
			return 0, fmt.Errorf("decode snapshot holding %s: %w", field.key, err)
		}
	}

	if !restoreNonnegative(quantity, price, valueTRY, valueUSD) ||
		(valueEUR != nil && !restoreNonnegative(*valueEUR)) {
		return 0, fmt.Errorf("%w: snapshot holding values", ErrIncompatibleBackup)
	}

	return id, nil
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
		valid = slices.Contains(
			[]string{restoreCurrencyTRY, restoreCurrencyUSD, restoreCurrencyEUR},
			value,
		)
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
