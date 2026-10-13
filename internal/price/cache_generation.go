package price

import (
	"context"
	"errors"
	"fmt"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
)

type quoteGenerationKey struct{}

var errQuoteSuperseded = errors.New("price refresh superseded by cache reset")

func quoteGeneration(ctx context.Context) int64 {
	generation, ok := ctx.Value(quoteGenerationKey{}).(int64)
	if !ok {
		return -1
	}

	return generation
}

func (service *Service) withQuoteGeneration(ctx context.Context) (context.Context, error) {
	err := ctx.Err()
	if err != nil {
		return ctx, fmt.Errorf("read price cache generation: %w", err)
	}

	expected, hasPrecondition := db.PortfolioGeneration(ctx)

	bound := quoteGeneration(ctx)
	if bound >= 0 && !hasPrecondition {
		return ctx, nil
	}

	var generation int64

	err = service.db.GetContext(
		ctx,
		&generation,
		"SELECT generation FROM snapshot_state WHERE id = 1",
	)
	if err != nil {
		return ctx, fmt.Errorf("read price cache generation: %w", err)
	}

	if hasPrecondition && (generation != expected || (bound >= 0 && bound != expected)) {
		return ctx, fmt.Errorf("read price cache generation: %w", db.ErrPortfolioChanged)
	}

	service.observeGeneration(generation)

	return context.WithValue(ctx, quoteGenerationKey{}, generation), nil
}

func (service *Service) observeGeneration(generation int64) {
	service.mu.Lock()
	defer service.mu.Unlock()

	if generation > service.generation {
		clear(service.failures)
		clear(service.lastFail)
		clear(service.lastErrors)
		clear(service.breakers)
		service.generation = generation
	}
}

func (service *Service) failureGenerationCurrent(ctx context.Context) bool {
	expected := quoteGeneration(ctx)
	if expected < 0 {
		return true
	}

	var generation int64

	err := service.db.GetContext(
		ctx,
		&generation,
		"SELECT generation FROM snapshot_state WHERE id = 1",
	)
	if err != nil {
		return false
	}

	service.observeGeneration(generation)

	return expected == generation
}

func holdingsNeedRefresh(holdings []models.Holding) bool {
	for index := range holdings {
		holding := &holdings[index]
		if holding.BaseCurrency != "" ||
			(holding.PriceProvider != "" && holding.PriceProvider != manualProviderName && holding.InstrumentID != "") {
			return true
		}
	}

	return false
}

// A restore can interrupt admitted work after its initial epoch check. Preserve
// the underlying error, but report a client conflict only if the epoch changed.
// Caller cancellation and shutdown retain their original errors.
func (service *Service) recheckClientQuoteGeneration(
	ctx context.Context,
	operationErr error,
) error {
	_, hasPrecondition := db.PortfolioGeneration(ctx)
	if !hasPrecondition || ctx.Err() != nil {
		return operationErr
	}

	_, generationErr := service.withQuoteGeneration(ctx)
	if errors.Is(generationErr, db.ErrPortfolioChanged) {
		return errors.Join(operationErr, generationErr)
	}

	return operationErr
}
