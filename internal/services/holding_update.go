package services

import (
	"context"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

const holdingColumns = `updated_at, created_at, manual_price_at,
COALESCE(price_provider, '') AS price_provider, COALESCE(instrument_id, '') AS instrument_id,
base_currency, COALESCE(basket, '') AS basket, COALESCE(location, '') AS location,
COALESCE(notes, '') AS notes, name, asset_type, COALESCE(ticker, '') AS ticker,
manual_price, id, quantity, COALESCE(est_price, 0) AS est_price`

func (service *HoldingService) Update(ctx context.Context, holding *models.Holding) error {
	if service.priceSvc != nil && holding.PriceProvider != "" &&
		!service.priceSvc.HasProvider(holding.PriceProvider) {
		return price.ErrUnknownProvider
	}

	tx, err := service.db.BeginTxx(ctx, nil)
	if err != nil {
		return wrapOperationError("begin Update", err)
	}
	defer func() {
		//nolint:errcheck // Rollback after a successful commit is harmless.
		_ = tx.Rollback()
	}()

	err = db.CheckPortfolioGeneration(ctx, tx)
	if err != nil {
		return wrapOperationError("Update", err)
	}

	var stored models.Holding

	err = tx.GetContext(ctx, &stored, `
                UPDATE holdings SET name = ?, ticker = ?, asset_type = ?, quantity = ?,
price_provider = ?, instrument_id = ?, base_currency = ?, basket = ?, location = ?, notes = ?,
est_price = ?, updated_at = ?
		WHERE id = ? RETURNING `+holdingColumns,
		holding.Name,
		holding.Ticker,
		holding.AssetType,
		holding.Quantity,
		holding.PriceProvider,
		holding.InstrumentID,
		holding.BaseCurrency,
		holding.Basket,
		holding.Location,
		holding.Notes,
		holding.EstPrice,
		time.Now().UTC(),
		holding.ID,
	)
	if err != nil {
		return wrapOperationError("Update", err)
	}

	err = tx.Commit()
	if err != nil {
		return wrapOperationError("commit Update", err)
	}
	// Return committed fields, including the retained manual override, never input-only quotes.
	*holding = stored

	return nil
}
