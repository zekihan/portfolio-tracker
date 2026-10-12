package price

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// GLOBAL_QUOTE omits currency. SYMBOL_SEARCH supplies it for an exact symbol;
// accepting the highest-scoring fuzzy match could silently misvalue a holding.
const alphaCurrencyTTL = 24 * time.Hour

var errUnknownQuoteCurrency = errors.New("unknown quote currency")

func (getter *AlphaVantage) quoteCurrency(ctx context.Context, symbol, key string) (string, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))

	getter.currencyMu.Lock()
	if getter.currencies == nil {
		getter.currencies = make(map[string]*sharedMetadata[string])
	}

	cache := getter.currencies[symbol]
	if cache == nil {
		// Evict only on insertion, preserving the rest of the scarce API metadata.
		if len(getter.currencies) >= maxPendingFetches {
			for cachedSymbol := range getter.currencies {
				delete(getter.currencies, cachedSymbol)

				break
			}
		}

		cache = &sharedMetadata[string]{}
		getter.currencies[symbol] = cache
	}
	getter.currencyMu.Unlock()
	// Currency metadata is stable and independent of quote freshness; avoid an
	// additional scarce API credit on every forced price refresh.
	ctx = context.WithValue(ctx, metadataRefreshKey{}, time.Time{})

	return cache.get(ctx, alphaCurrencyTTL, func(fetchCtx context.Context) (string, error) {
		return getter.fetchQuoteCurrency(fetchCtx, symbol, key)
	})
}

//nolint:tagliatelle // Alpha Vantage defines these upstream field names.
func (getter *AlphaVantage) fetchQuoteCurrency(
	ctx context.Context,
	symbol, key string,
) (string, error) {
	target := "https://www.alphavantage.co/query?function=SYMBOL_SEARCH&keywords=" +
		url.QueryEscape(symbol) + "&apikey=" + url.QueryEscape(key)

	body, err := providerRequest(ctx, getter.client, target, nil)
	if err != nil {
		return "", err
	}

	var result struct {
		Note         string `json:"Note"`
		Information  string `json:"Information"`
		ErrorMessage string `json:"Error Message"`
		Matches      []struct {
			Symbol   string `json:"1. symbol"`
			Currency string `json:"8. currency"`
		} `json:"bestMatches"`
	}

	err = json.Unmarshal(body, &result)
	if err != nil {
		return "", wrapOperationError("quoteCurrency", err)
	}

	if result.Note != "" {
		return "", fmt.Errorf("%w: %s", errRateLimited, result.Note)
	}

	if result.Information != "" || result.ErrorMessage != "" {
		return "", errAlphaVantageAPIErrorFor
	}

	currency := ""

	for _, match := range result.Matches {
		if strings.EqualFold(match.Symbol, strings.TrimSpace(symbol)) &&
			strings.TrimSpace(match.Currency) != "" {
			if currency != "" && currency != strings.TrimSpace(match.Currency) {
				return "", instrumentFailure(errUnknownQuoteCurrency)
			}

			currency = strings.TrimSpace(match.Currency)
		}
	}

	if currency == "" {
		return "", instrumentFailure(errUnknownQuoteCurrency)
	}

	return currency, nil
}
