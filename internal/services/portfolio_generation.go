package services

import (
	"context"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

// PortfolioGeneration identifies data exposed by an HTTP read.
func (service *HoldingService) PortfolioGeneration(ctx context.Context) (int64, error) {
	generation, err := db.ReadPortfolioGeneration(ctx, service.db)

	return generation, wrapOperationError("PortfolioGeneration", err)
}

// PortfolioGeneration identifies data exposed by an HTTP read.
func (service *SettingsService) PortfolioGeneration(ctx context.Context) (int64, error) {
	generation, err := db.ReadPortfolioGeneration(ctx, service.db)

	return generation, wrapOperationError("PortfolioGeneration", err)
}

// PortfolioGeneration identifies data exposed by an HTTP read.
func (service *SnapshotService) PortfolioGeneration(ctx context.Context) (int64, error) {
	generation, err := db.ReadPortfolioGeneration(ctx, service.db)

	return generation, wrapOperationError("PortfolioGeneration", err)
}
