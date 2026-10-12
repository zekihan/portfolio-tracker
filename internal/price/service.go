package price

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

const breakerCooldown = 5 * time.Minute

type Service struct {
	db       *sqlx.DB
	registry *Registry
	failures map[string]int
	lastFail map[string]time.Time
	breakers map[string]bool
	mu       sync.RWMutex
}

func NewService(database *sqlx.DB, registry *Registry) *Service {
	return &Service{
		db:       database,
		registry: registry,
		failures: make(map[string]int),
		lastFail: make(map[string]time.Time),
		breakers: make(map[string]bool),
	}
}

func (service *Service) GetPrice(
	ctx context.Context,
	providerName, instrumentID string,
) (models.Price, error) {
	return service.getPrice(ctx, providerName, instrumentID, false)
}

func canUseCachedQuote(cached *models.PriceCacheEntry, forceRefresh bool) bool {
	return !forceRefresh && cached != nil && !cached.ExpiresAt.Before(time.Now())
}

func (service *Service) GetUSDTTRYRate(ctx context.Context) (float64, error) {
	// Try Frankfurter first.
	quote, err := service.GetPrice(ctx, "Frankfurter", "USD")
	if err == nil && quote.Value > 0 && !math.IsInf(quote.Value, 0) {
		return quote.Value, nil
	}
	// Fallback to Yahoo Finance FX.
	quote, err = service.GetPrice(ctx, "Yahoo Finance", "USDTRY=X")
	if err == nil {
		if quote.Value > 0 && !math.IsInf(quote.Value, 0) {
			return quote.Value, nil
		}

		return 0, fmt.Errorf("%w: %v", errInvalidFXRateFromYahooFinance, quote.Value)
	}

	return 0, fmt.Errorf("no FX rate available: %w", err)
}

func (service *Service) RefreshAll(ctx context.Context, holdings []models.Holding) error {
	var refreshErrors []error

	seen := make(map[[2]string]bool)

	for index := range holdings {
		holding := holdings[index]

		if ctx.Err() != nil {
			break
		}

		if holding.PriceProvider == "" || holding.PriceProvider == "Manual" {
			continue
		}

		if holding.InstrumentID == "" {
			continue
		}

		key := [2]string{holding.PriceProvider, holding.InstrumentID}
		if seen[key] {
			continue
		}

		seen[key] = true

		fetched, err := service.getPrice(ctx, holding.PriceProvider, holding.InstrumentID, true)
		if err != nil {
			refreshErrors = append(refreshErrors,
				fmt.Errorf("refresh %s/%s: %w", holding.PriceProvider, holding.InstrumentID, err))
		} else if fetched.IsFailed {
			refreshErrors = append(
				refreshErrors,
				fmt.Errorf(
					"%w %s/%s failed: using stale cached data",
					errRefresh,
					holding.PriceProvider,
					holding.InstrumentID,
				),
			)
		}
	}

	return errors.Join(errors.Join(refreshErrors...), ctx.Err())
}

func (service *Service) TestProvider(
	ctx context.Context,
	providerName, instrumentID string,
) (models.Price, error) {
	if ctx.Err() != nil {
		return models.Price{}, wrapOperationError("TestProvider", ctx.Err())
	}

	getter, ok := service.registry.Get(providerName)
	if !ok {
		return models.Price{}, fmt.Errorf("%w %s not found", errProvider, providerName)
	}

	quote, err := getter.GetPrice(ctx, instrumentID)
	if ctx.Err() != nil {
		return models.Price{}, wrapOperationError("TestProvider", ctx.Err())
	}

	return quote, wrapOperationError("TestProvider", err)
}

func (service *Service) GetProviderHealth() ([]models.ProviderHealth, error) {
	return service.GetProviderHealthContext(context.Background())
}

func (service *Service) GetProviderHealthContext(
	ctx context.Context,
) ([]models.ProviderHealth, error) {
	var providers []models.Provider

	err := service.db.SelectContext(
		ctx,
		&providers,
		`SELECT updated_at, created_at, name, type, key_env_var, base_url, ttl_seconds, id, enabled,
key_required FROM provider_registry WHERE enabled = 1 ORDER BY name`,
	)
	if err != nil {
		return nil, wrapOperationError("GetProviderHealthContext", err)
	}

	health := make([]models.ProviderHealth, 0, len(providers))
	for index := range providers {
		provider := providers[index]

		service.mu.RLock()
		failures := service.failures[provider.Name]
		breaker := service.breakers[provider.Name]
		service.mu.RUnlock()

		// Get last successful fetch from cache.
		var lastOK sql.NullTime

		err = service.db.GetContext(ctx, &lastOK, `
			SELECT fetched_at FROM price_cache
			WHERE provider = ? AND error_count = 0
			ORDER BY fetched_at DESC LIMIT 1
		`,
			provider.Name,
		)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, wrapOperationError("GetProviderHealthContext", err)
		}

		status := "✓ Active"
		errorMsg := ""

		if breaker {
			status = "⚠ FAILED 3×"
			errorMsg = "Circuit breaker open"
		} else if failures > 0 {
			status = "⚠ Errors"
			// Get last error.
			var lastErr string

			err = service.db.GetContext(
				ctx,
				&lastErr,
				"SELECT last_error FROM price_cache WHERE provider = ? ORDER BY fetched_at DESC LIMIT 1",
				provider.Name,
			)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, wrapOperationError("GetProviderHealthContext", err)
			}

			errorMsg = lastErr
		}

		health = append(health, models.ProviderHealth{
			Name:        provider.Name,
			Type:        provider.Type,
			KeyRequired: provider.KeyRequired,
			Status:      status,
			LastOK:      lastOK.Time,
			ErrorMsg:    errorMsg,
		})
	}

	return health, nil
}

func (service *Service) GetHoldingProviderMapping() ([]HoldingProviderMapping, error) {
	return service.GetHoldingProviderMappingContext(context.Background())
}

func (service *Service) GetHoldingProviderMappingContext(
	ctx context.Context,
) ([]HoldingProviderMapping, error) {
	var holdings []models.Holding

	err := service.db.SelectContext(
		ctx,
		&holdings,
		"SELECT id, name, price_provider, instrument_id FROM holdings ORDER BY name",
	)
	if err != nil {
		return nil, wrapOperationError("GetHoldingProviderMappingContext", err)
	}

	mappings := make([]HoldingProviderMapping, 0, len(holdings))
	for index := range holdings {
		holding := holdings[index]
		mappings = append(mappings, HoldingProviderMapping{
			HoldingID:    holding.ID,
			HoldingName:  holding.Name,
			Provider:     holding.PriceProvider,
			InstrumentID: holding.InstrumentID,
		})
	}

	return mappings, nil
}

type HoldingProviderMapping struct {
	HoldingName  string `json:"holding_name"`
	Provider     string `json:"provider"`
	InstrumentID string `json:"instrument_id"`
	HoldingID    int    `json:"holding_id"`
}

func (service *Service) UpdateHoldingProvider(holdingID int, provider, instrumentID string) error {
	return service.UpdateHoldingProviderContext(
		context.Background(),
		holdingID,
		provider,
		instrumentID,
	)
}

func (service *Service) UpdateHoldingProviderContext(
	ctx context.Context, holdingID int, provider, instrumentID string,
) error {
	_, err := service.db.ExecContext(ctx, `
		UPDATE holdings SET price_provider = ?, instrument_id = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`,
		provider,
		instrumentID,
		holdingID,
	)

	return wrapOperationError("UpdateHoldingProviderContext", err)
}

func (service *Service) ClearCache() error {
	_, err := service.db.ExecContext(context.Background(), "DELETE FROM price_cache")

	return wrapOperationError("ClearCache", err)
}

func (service *Service) GetProvidersJSON() (string, error) {
	providers, err := service.registry.GetProvidersJSON()
	if err != nil {
		return "", err
	}

	data, err := json.Marshal(providers)
	if err != nil {
		return "", fmt.Errorf("encode providers: %w", err)
	}

	return string(data), nil
}

func (registry *Registry) GetProvidersJSON() ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(registry.getters))
	for _, g := range registry.All() {
		result = append(result, map[string]any{
			"name":        g.Name(),
			"type":        g.Type(),
			"ttl_seconds": int(g.TTL().Seconds()),
		})
	}

	return result, nil
}

var (
	errRefresh                       = errors.New("refresh")
	errProvider                      = errors.New("provider")
	errCircuitBreakerOpenFor         = errors.New("circuit breaker open for")
	errInvalidFXRateFromYahooFinance = errors.New("invalid FX rate from Yahoo Finance")
)

const (
	defaultCacheLifetime    = 24 * time.Hour
	breakerFailureThreshold = 3
)

func (service *Service) getPrice(
	ctx context.Context,
	providerName, instrumentID string,
	forceRefresh bool,
) (models.Price, error) {
	if ctx.Err() != nil {
		return models.Price{}, wrapOperationError("getPrice", ctx.Err())
	}

	getter, ok := service.registry.Get(providerName)
	if !ok {
		return models.Price{}, fmt.Errorf("%w %s not found", errProvider, providerName)
	}

	// Check cache first.
	cached, err := service.getCachedPriceContext(ctx, providerName, instrumentID)
	if ctx.Err() != nil {
		return models.Price{}, wrapOperationError("getPrice", ctx.Err())
	}

	if err == nil && canUseCachedQuote(cached, forceRefresh) {
		quote := models.Price{
			Value:     cached.Price,
			Currency:  cached.Currency,
			Source:    cached.Source,
			FetchedAt: cached.FetchedAt,
			IsStale:   false,
		}

		return quote, nil
	}

	// Check circuit breaker.
	if service.breakerBlocksRequest(providerName) {
		// Return stale cached data if available.
		if cached != nil {
			quote := models.Price{
				Value:     cached.Price,
				Currency:  cached.Currency,
				Source:    cached.Source,
				FetchedAt: cached.FetchedAt,
				IsStale:   true,
				IsFailed:  true,
			}

			return quote, nil
		}

		return models.Price{}, fmt.Errorf("%w %s", errCircuitBreakerOpenFor, providerName)
	}

	// Fetch fresh price.
	quote, err := getter.GetPrice(ctx, instrumentID)
	if ctx.Err() != nil {
		return models.Price{}, wrapOperationError("getPrice", ctx.Err())
	}

	if err != nil {
		service.recordFailureContext(ctx, providerName, err)

		if ctx.Err() != nil {
			return models.Price{}, wrapOperationError(

				// Return stale cached data if available.
				"getPrice", ctx.Err())
		}

		if cached != nil {
			quote = models.Price{
				Value:     cached.Price,
				Currency:  cached.Currency,
				Source:    cached.Source,
				FetchedAt: cached.FetchedAt,
				IsStale:   true,
				IsFailed:  true,
			}

			return quote, nil
		}

		return models.Price{}, wrapOperationError("getPrice", err)
	}

	err = service.storeFetchedQuote(ctx, getter, instrumentID, &quote, forceRefresh)
	if err != nil {
		return models.Price{}, err
	}

	return quote, nil
}

func (service *Service) storeFetchedQuote(
	ctx context.Context, getter Getter, instrument string, quote *models.Price, forceRefresh bool,
) error {
	// Cache before recording success so expired requests cannot reset health.
	err := service.cachePriceContext(ctx, getter.Name(), instrument, quote, getter.TTL())
	if ctx.Err() != nil {
		return wrapOperationError(

			// Explicit refresh must persist the quote; ordinary lookups can still use it.
			"storeFetchedQuote", ctx.Err())
	}

	if forceRefresh && err != nil {
		return fmt.Errorf("persist refreshed quote: %w", err)
	}

	service.recordSuccess(getter.Name())

	return nil
}

func (service *Service) getCachedPrice(
	provider, instrumentID string,
) (*models.PriceCacheEntry, error) {
	return service.getCachedPriceContext(context.Background(), provider, instrumentID)
}

func (service *Service) getCachedPriceContext(
	ctx context.Context, provider, instrumentID string,
) (*models.PriceCacheEntry, error) {
	var entry models.PriceCacheEntry

	err := service.db.GetContext(ctx, &entry, `
                SELECT fetched_at, expires_at, provider, instrument_id, currency, source,
last_error, id, price, error_count FROM price_cache WHERE provider = ? AND instrument_id = ?`,
		provider,
		instrumentID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // Absence is a successful lookup result.
	}

	if err != nil {
		return nil, wrapOperationError("getCachedPriceContext", err)
	}

	return &entry, nil
}

func (service *Service) cachePrice(
	provider, instrumentID string,
	quote *models.Price,
	ttl time.Duration,
) error {
	return service.cachePriceContext(context.Background(), provider, instrumentID, quote, ttl)
}

func (service *Service) cachePriceContext(
	ctx context.Context, provider, instrumentID string,
	quote *models.Price, ttl time.Duration,
) error {
	expiresAt := time.Now().Add(ttl)
	if ttl == 0 {
		expiresAt = time.Now().Add(defaultCacheLifetime)
	}

	_, err := service.db.ExecContext(ctx, `
                INSERT INTO price_cache (provider, instrument_id, price, currency, source,
fetched_at, expires_at, error_count, last_error)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, '')
		ON CONFLICT(provider, instrument_id) DO UPDATE SET
			price = excluded.price,
			currency = excluded.currency,
			source = excluded.source,
			fetched_at = excluded.fetched_at,
			expires_at = excluded.expires_at,
			error_count = 0,
			last_error = ''
	`,
		provider,
		instrumentID,
		quote.Value,
		quote.Currency,
		quote.Source,
		quote.FetchedAt.UTC().Format(time.RFC3339),
		expiresAt.UTC().Format(time.RFC3339),
	)

	return wrapOperationError("cachePriceContext", err)
}

func (service *Service) recordFailure(provider string, err error) {
	service.recordFailureContext(context.Background(), provider, err)
}

func (service *Service) recordFailureContext(ctx context.Context, provider string, err error) {
	service.mu.Lock()
	service.failures[provider]++
	service.lastFail[provider] = time.Now()

	// Open circuit breaker after 3 failures.
	if service.failures[provider] >= breakerFailureThreshold {
		service.breakers[provider] = true
		slog.WarnContext(
			ctx,
			"Circuit breaker opened",
			slog.String("provider", provider),
			slog.Int("failures", service.failures[provider]),
		)
	}

	service.mu.Unlock()

	// Database waits must not block other providers' health state.
	_, persistErr := service.db.ExecContext(ctx, `
		UPDATE price_cache SET error_count = error_count + 1, last_error = ?
		WHERE provider = ?
	`,
		err.Error(),
		provider,
	)
	if persistErr != nil {
		slog.WarnContext(ctx, "Failed to persist provider failure", slog.Any("error", persistErr))
	}
}

// Retry after cooldown; a failed retry renews it and success closes the breaker.
func (service *Service) breakerBlocksRequest(provider string) bool {
	service.mu.RLock()
	defer service.mu.RUnlock()

	lastFailure := service.lastFail[provider]

	return service.breakers[provider] &&
		(lastFailure.IsZero() || time.Since(lastFailure) < breakerCooldown)
}

func (service *Service) recordSuccess(provider string) {
	service.mu.Lock()
	defer service.mu.Unlock()

	service.failures[provider] = 0
	service.breakers[provider] = false
}
