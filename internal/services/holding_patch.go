package services

import (
	"context"
	"fmt"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

// Patch updates supplied editable fields atomically without reading/replacing the holding.
func (service *HoldingService) Patch(
	ctx context.Context,
	id int,
	patch *models.HoldingPatch,
) (bool, error) {
	if patch.PriceProvider != nil && *patch.PriceProvider != "" && service.priceSvc != nil &&
		!service.priceSvc.HasProvider(*patch.PriceProvider) {
		return false, price.ErrUnknownProvider
	}

	result, err := db.ExecPortfolioMutation(
		ctx, service.db,
		`UPDATE holdings SET
 name = COALESCE(?, name), ticker = COALESCE(?, ticker),
 basket = COALESCE(?, basket), location = COALESCE(?, location), notes = COALESCE(?, notes),
 quantity = COALESCE(?, quantity), est_price = COALESCE(?, est_price),
 asset_type = COALESCE(?, asset_type), price_provider = COALESCE(?, price_provider),
 instrument_id = COALESCE(?, instrument_id), base_currency = COALESCE(?, base_currency),
 updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		patch.Name,
		patch.Ticker,
		patch.Basket,
		patch.Location,
		patch.Notes,
		patch.Quantity,
		patch.EstPrice,
		patch.AssetType,
		patch.PriceProvider,
		patch.InstrumentID,
		patch.BaseCurrency,
		id,
	)
	if err != nil {
		return false, fmt.Errorf("patch holding: %w", err)
	}

	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count patched holdings: %w", err)
	}

	return count > 0, nil
}
