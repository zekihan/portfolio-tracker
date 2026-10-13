package services

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

const (
	valuationBudget  = 20 * time.Second
	valuationWorkers = 4
)

type valuationScope uint8

const (
	currentValuesOnly valuationScope = iota
	currentAndEstimatedValues
)

func (service *HoldingService) getWithPrices(
	ctx context.Context,
	displayCurrency string,
	budget time.Duration,
) ([]models.HoldingWithPrice, error) {
	holdings, _, _, err := service.getValuations(
		ctx,
		budget,
		displayCurrency,
		currentAndEstimatedValues,
	)

	return holdings, err
}

//nolint:gocyclo // Prioritized quote phases and optional FX preserve partial valuations safely.
func (service *HoldingService) getValuations(
	ctx context.Context,
	budget time.Duration,
	displayCurrency string,
	scope valuationScope,
) ([]models.HoldingWithPrice, models.Price, models.Price, error) {
	valuationCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	holdings, err := service.GetAll(valuationCtx)
	if err != nil {
		return nil, models.Price{}, models.Price{}, err
	}

	fxQuote, err := service.priceSvc.GetUSDTTRYQuote(valuationCtx)
	if err != nil {
		return nil, models.Price{}, models.Price{}, wrapOperationError("GetWithPrices", err)
	}

	// Fixed workers bound provider requests and goroutines; indexed results preserve order.
	result := make([]models.HoldingWithPrice, len(holdings))
	for index := range holdings {
		result[index] = models.HoldingWithPrice{Holding: holdings[index]}
		result[index].IsFailed = true
		result[index].PriceError = "valuation time budget exceeded"

		if !needsAutomaticQuote(&holdings[index]) {
			result[index], err = service.safeValueHolding(
				valuationCtx,
				&holdings[index],
				fxQuote.Value,
			)
		} else {
			err = service.normalizeCurrentHolding(&result[index], fxQuote.Value)
		}

		if err != nil {
			return nil, models.Price{}, models.Price{}, err
		}
	}

	err = service.runValuations(valuationCtx, holdings, result, fxQuote.Value, false)

	if ctx.Err() != nil {
		return nil, models.Price{}, models.Price{}, wrapOperationError("GetWithPrices", ctx.Err())
	}

	if err != nil {
		return nil, models.Price{}, models.Price{}, err
	}

	var (
		eurQuote models.Price
		eurErr   error
	)

	requiredEUR := scope.requiresEURValuation(result, displayCurrency)
	if requiredEUR {
		eurQuote, eurErr = service.eurValuationQuote(
			ctx,
			valuationCtx,
			requiredEUR,
			displayCurrency,
		)
	}

	err = service.runValuations(valuationCtx, holdings, result, fxQuote.Value, true)
	if err != nil {
		return nil, models.Price{}, models.Price{}, err
	}

	if !requiredEUR {
		eurQuote, eurErr = service.eurValuationQuote(
			ctx,
			valuationCtx,
			requiredEUR,
			displayCurrency,
		)
	}

	if ctx.Err() != nil {
		return nil, models.Price{}, models.Price{}, wrapOperationError("GetWithPrices", ctx.Err())
	}

	if eurErr != nil && displayCurrency == restoreCurrencyEUR {
		return nil, models.Price{}, models.Price{}, eurErr
	}

	err = service.finishCurrencyValuations(
		result,
		&fxQuote,
		&eurQuote,
		eurErr,
		displayCurrency,
		scope,
	)
	if err != nil {
		return nil, models.Price{}, models.Price{}, err
	}

	return result, fxQuote, eurQuote, nil
}

var (
	errNonFiniteHoldingValue = errors.New("non-finite holding valuation")
	errMissingQuote          = errors.New("no usable quote available")
)

// safeValueHolding isolates unexpected worker failures from unrelated holdings.
//
//nolint:nonamedreturns // Recovery must replace the worker result after a panic.
func (service *HoldingService) safeValueHolding(
	ctx context.Context,
	holding *models.Holding,
	rate float64,
) (result models.HoldingWithPrice, err error) {
	defer func() {
		if recover() != nil {
			result = models.HoldingWithPrice{Holding: *holding}
			result.IsFailed = true
			result.PriceError = "Price unavailable"
			err = nil
		}
	}()

	return service.valueHolding(ctx, holding, rate)
}

//nolint:gocyclo,nestif // Manual overrides and remote quote failures have distinct valuation semantics.
func (service *HoldingService) valueHolding(
	ctx context.Context,
	holding *models.Holding,
	rate float64,
) (models.HoldingWithPrice, error) {
	hwp := models.HoldingWithPrice{Holding: *holding}
	// A manual price overrides the configured automatic provider.
	if holding.ManualPrice.Valid && holding.ManualPrice.Float64 > 0 {
		hwp.CurrentPrice = holding.ManualPrice.Float64
		hwp.CurrentCurrency = holding.BaseCurrency
		hwp.PriceSource = "Manual"
		hwp.PriceFetchedAt = holding.ManualPriceAt.Time
		hwp.ValuationAvailable = true
	} else if holding.PriceProvider != "" && holding.PriceProvider != "Manual" && holding.InstrumentID != "" {
		quote, quoteErr := service.priceSvc.GetPrice(
			ctx,
			holding.PriceProvider,
			holding.InstrumentID,
		)
		if quoteErr == nil {
			hwp.CurrentPrice = quote.Value
			hwp.CurrentCurrency = quote.Currency
			hwp.PriceSource = quote.Source
			hwp.PriceFetchedAt = quote.FetchedAt
			hwp.IsStale = quote.IsStale
			hwp.IsFailed = quote.IsFailed

			hwp.ValuationAvailable = quote.Value > 0
			if quote.Error != nil {
				hwp.PriceError = "Price refresh unavailable"
			}
		} else {
			hwp.PriceError = "Price unavailable"
			if errors.Is(quoteErr, context.DeadlineExceeded) {
				hwp.PriceError = "Refresh timed out"
			}
		}
	}

	if hwp.ValuationAvailable && hwp.CurrentCurrency != "TRY" && hwp.CurrentCurrency != "USD" &&
		hwp.CurrentCurrency != restoreCurrencyEUR {
		hwp.ValuationAvailable = false
		hwp.PriceError = errUnsupportedQuoteCurrency.Error()
	}

	if !hwp.ValuationAvailable {
		hwp.IsFailed = true
		if hwp.PriceError == "" {
			hwp.PriceError = errMissingQuote.Error()
		}
	}

	err := service.normalizeCurrentHolding(&hwp, rate)

	return hwp, err
}

func needsAutomaticQuote(holding *models.Holding) bool {
	return !(holding.ManualPrice.Valid && holding.ManualPrice.Float64 > 0) &&
		holding.PriceProvider != "" && holding.PriceProvider != "Manual" && holding.InstrumentID != ""
}

func (service *HoldingService) runValuations(
	ctx context.Context,
	holdings []models.Holding,
	result []models.HoldingWithPrice,
	rate float64,
	zeroQuantity bool,
) error {
	errorsByIndex := make([]error, len(holdings))
	jobs := make(chan int)

	var workers sync.WaitGroup
	for range min(valuationWorkers, len(holdings)) {
		workers.Go(func() {
			for index := range jobs {
				if ctx.Err() != nil {
					continue
				}

				result[index], errorsByIndex[index] = service.safeValueHolding(
					ctx,
					&holdings[index],
					rate,
				)
			}
		})
	}

	// Each phase preserves row indexes and shares the same worker and time bounds.
queue:
	for index := range holdings {
		if (holdings[index].Quantity == 0) != zeroQuantity ||
			!needsAutomaticQuote(&holdings[index]) {
			continue
		}

		select {
		case jobs <- index:
		case <-ctx.Done():
			break queue
		}
	}

	close(jobs)
	workers.Wait()

	for _, err := range errorsByIndex {
		if err != nil {
			return err
		}
	}

	return nil
}

// finishCurrencyValuations applies only the exchange rates used by each row.
//
//nolint:gocyclo // Per-row quote and estimate dependencies carry independent quality flags.
func (service *HoldingService) finishCurrencyValuations(
	result []models.HoldingWithPrice,
	fxQuote, eurQuote *models.Price,
	eurErr error,
	displayCurrency string,
	scope valuationScope,
) error {
	for index := range result {
		row := &result[index]
		if scope == currentAndEstimatedValues && row.Quantity != 0 &&
			row.BaseCurrency == restoreCurrencyEUR && row.EstPrice > 0 &&
			eurQuote.Value <= 0 {
			return eurErr
		}

		if row.CurrentCurrency == restoreCurrencyEUR && eurQuote.Value <= 0 {
			row.ValuationAvailable = false
			row.IsFailed = true
			row.PriceError = "EUR exchange rate unavailable"
		}

		var err error
		if scope == currentAndEstimatedValues {
			err = service.normalizeHolding(row, fxQuote.Value, eurQuote.Value)
		} else {
			err = service.normalizeCurrentHolding(row, fxQuote.Value, eurQuote.Value)
		}

		if err != nil {
			return err
		}

		if row.ValuationAvailable &&
			(displayCurrency == restoreCurrencyEUR || scope.holdingNeedsEUR(row)) {
			row.IsStale = row.IsStale || eurQuote.IsStale

			row.IsFailed = row.IsFailed || eurQuote.IsFailed
			if eurQuote.IsFailed && row.PriceError == "" {
				row.PriceError = "EUR exchange rate refresh unavailable"
			}
		}

		if result[index].ValuationAvailable {
			result[index].IsStale = result[index].IsStale || fxQuote.IsStale

			result[index].IsFailed = result[index].IsFailed || fxQuote.IsFailed
			if fxQuote.IsFailed && result[index].PriceError == "" {
				result[index].PriceError = "Exchange rate refresh unavailable"
			}
		}
	}

	return nil
}
