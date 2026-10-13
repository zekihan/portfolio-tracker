package price

import (
	"context"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

// PortfolioGeneration identifies data exposed by an HTTP read.
func (service *Service) PortfolioGeneration(ctx context.Context) (int64, error) {
	generation, err := db.ReadPortfolioGeneration(ctx, service.db)

	return generation, wrapOperationError("PortfolioGeneration", err)
}
