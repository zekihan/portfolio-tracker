package services

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

var (
	ErrInvalidIdempotencyKey = errors.New(
		"Idempotency-Key must contain 1–128 letters, digits, hyphens or underscores",
	)
	ErrHoldingIdempotencyConflict = errors.New(
		"this Add attempt already created a holding with different details; reload to check it before adding again",
	)
)

func (service *HoldingService) Create(ctx context.Context, holding *models.Holding) error {
	return service.CreateWithIdempotencyKey(ctx, holding, "")
}

// CreateWithIdempotencyKey saves both the holding and its receipt atomically.
// An empty key retains the original API's non-idempotent create behavior.
func (service *HoldingService) CreateWithIdempotencyKey(
	ctx context.Context,
	holding *models.Holding,
	key string,
) error {
	err := service.validateHoldingCreate(holding, key)
	if err != nil {
		return err
	}

	tx, err := service.db.BeginTxx(ctx, nil)
	if err != nil {
		return wrapOperationError("begin Create", err)
	}
	defer func() {
		//nolint:errcheck // Rollback after a successful commit is harmless.
		_ = tx.Rollback()
	}()

	err = db.CheckPortfolioGeneration(ctx, tx)
	if err != nil {
		return wrapOperationError("check Create generation", err)
	}

	if key == "" {
		err = insertHolding(ctx, tx, holding)
	} else {
		err = createHoldingWithReceipt(ctx, tx, holding, key)
	}

	if err != nil {
		return err
	}

	return wrapOperationError("commit Create", tx.Commit())
}

func (service *HoldingService) validateHoldingCreate(holding *models.Holding, key string) error {
	if !validHoldingCreateKey(key) {
		return ErrInvalidIdempotencyKey
	}

	if service.priceSvc != nil && holding.PriceProvider != "" &&
		!service.priceSvc.HasProvider(holding.PriceProvider) {
		return price.ErrUnknownProvider
	}

	if holding.ManualPrice.Valid &&
		(holding.ManualPrice.Float64 < 0 || math.IsNaN(holding.ManualPrice.Float64) ||
			math.IsInf(holding.ManualPrice.Float64, 0)) {
		return errManualPriceMustBeFiniteAndNonnegative
	}

	return nil
}

func createHoldingWithReceipt(
	ctx context.Context,
	tx *sqlx.Tx,
	holding *models.Holding,
	key string,
) error {
	// Take the writer lock even for trusted internal callers without a client fence.
	// No concurrent process may insert between our receipt lookup and creation.
	var generation int64

	err := tx.GetContext(
		ctx,
		&generation,
		"UPDATE snapshot_state SET generation = generation WHERE id = 1 RETURNING generation",
	)
	if err != nil {
		return wrapOperationError("lock Create receipt", err)
	}

	hash, err := holdingCreateHash(holding)
	if err != nil {
		return err
	}

	found, err := replayHoldingCreate(ctx, tx, generation, key, hash, holding)
	if err != nil || found {
		return err
	}

	err = insertHolding(ctx, tx, holding)
	if err != nil {
		return err
	}

	response, err := json.Marshal(holding)
	if err != nil {
		return wrapOperationError("encode Create receipt", err)
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO holding_create_receipts
		(generation, idempotency_key, request_hash, response_json) VALUES (?, ?, ?, ?)`,
		generation, key, hash, string(response))

	return wrapOperationError("save Create receipt", err)
}

func holdingCreateHash(holding *models.Holding) (string, error) {
	manualPrice := 0.0
	if holding.ManualPrice.Valid {
		manualPrice = holding.ManualPrice.Float64
	}

	// Only writable fields define a request. Ignore server timestamps, IDs and quotes.
	request := models.Holding{
		Name: holding.Name, Ticker: holding.Ticker, AssetType: holding.AssetType,
		Quantity: holding.Quantity, PriceProvider: holding.PriceProvider,
		InstrumentID: holding.InstrumentID, BaseCurrency: holding.BaseCurrency,
		Basket: holding.Basket, Location: holding.Location, Notes: holding.Notes,
		EstPrice: holding.EstPrice, ManualPrice: sql.NullFloat64{Float64: manualPrice, Valid: true},
	}

	encoded, err := json.Marshal(request)
	if err != nil {
		return "", wrapOperationError("hash Create request", err)
	}

	return fmt.Sprintf("%x", sha256.Sum256(encoded)), nil
}

func replayHoldingCreate(
	ctx context.Context,
	tx *sqlx.Tx,
	generation int64,
	key, hash string,
	holding *models.Holding,
) (bool, error) {
	var receipt struct {
		RequestHash  string `db:"request_hash"`
		ResponseJSON string `db:"response_json"`
	}

	err := tx.GetContext(
		ctx,
		&receipt,
		`SELECT request_hash, response_json FROM holding_create_receipts
		WHERE generation = ? AND idempotency_key = ?`,
		generation,
		key,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, wrapOperationError("read Create receipt", err)
	}

	if receipt.RequestHash != hash {
		return false, ErrHoldingIdempotencyConflict
	}

	err = json.Unmarshal([]byte(receipt.ResponseJSON), holding)

	return true, wrapOperationError("decode Create receipt", err)
}

func insertHolding(ctx context.Context, tx *sqlx.Tx, holding *models.Holding) error {
	now := time.Now().UTC()

	manualPrice := 0.0
	if holding.ManualPrice.Valid {
		manualPrice = holding.ManualPrice.Float64
	}

	holding.ManualPriceAt = sql.NullTime{Time: now, Valid: manualPrice > 0}
	holding.CreatedAt = now
	holding.UpdatedAt = now

	result, err := tx.ExecContext(ctx, `
		INSERT INTO holdings (name, ticker, asset_type, quantity, price_provider,
		instrument_id, base_currency, basket, location, notes, est_price, manual_price, manual_price_at,
		created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		holding.Name, holding.Ticker, holding.AssetType, holding.Quantity, holding.PriceProvider,
		holding.InstrumentID, holding.BaseCurrency, holding.Basket, holding.Location, holding.Notes,
		holding.EstPrice, manualPrice, holding.ManualPriceAt, holding.CreatedAt, holding.UpdatedAt)
	if err != nil {
		return wrapOperationError("Create", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return wrapOperationError("Create", err)
	}

	holding.ID = int(id)

	return nil
}

func validHoldingCreateKey(key string) bool {
	const (
		maxKeyLength = 128
		allowed      = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	)

	return len(key) <= maxKeyLength && strings.Trim(key, allowed) == ""
}
