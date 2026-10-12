package services

import (
	"context"
	"fmt"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

// Patch updates supplied editable fields atomically without reading/replacing the holding.
func (service *HoldingService) Patch(
	ctx context.Context,
	id int,
	patch models.HoldingPatch,
) (bool, error) {
	result, err := service.db.ExecContext(
		ctx,
		`UPDATE holdings SET
 name = COALESCE(?, name), ticker = COALESCE(?, ticker),
 basket = COALESCE(?, basket), location = COALESCE(?, location), notes = COALESCE(?, notes),
 quantity = COALESCE(?, quantity), est_price = COALESCE(?, est_price),
 updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		patch.Name,
		patch.Ticker,
		patch.Basket,
		patch.Location,
		patch.Notes,
		patch.Quantity,
		patch.EstPrice,
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
