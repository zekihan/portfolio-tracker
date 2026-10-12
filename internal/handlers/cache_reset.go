package handlers

import (
	"context"
	"fmt"
)

// withPriceCacheReset prevents a pre-reset quote from repopulating replaced data.
func (handler *Handler) withPriceCacheReset(ctx context.Context, mutate func() error) error {
	if handler.priceSvc == nil {
		return mutate()
	}

	err := handler.priceSvc.WithCacheReset(ctx, mutate)
	if err != nil {
		return fmt.Errorf("reset price cache: %w", err)
	}

	return nil
}
