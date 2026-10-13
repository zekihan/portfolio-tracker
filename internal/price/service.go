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

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
)

const breakerCooldown = 5 * time.Minute

type Service struct {
	db         *sqlx.DB
	registry   *Registry
	failures   map[string]int
	lastFail   map[string]time.Time
	lastErrors map[string]string
	breakers   map[string]bool
	refresh    refreshState
	mu         sync.RWMutex
	generation int64
}

func NewService(database *sqlx.DB, registry *Registry) *Service {
	service := &Service{
		db:         database,
		registry:   registry,
		failures:   make(map[string]int),
		lastFail:   make(map[string]time.Time),
		lastErrors: make(map[string]string),
		breakers:   make(map[string]bool),
	}
	service.initRefresh()

	return service
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
	quote, err := service.GetUSDTTRYQuote(ctx)

	return quote.Value, err
}

// GetUSDTTRYQuote preserves freshness and failure metadata for valuation callers.
func (service *Service) GetUSDTTRYQuote(ctx context.Context) (models.Price, error) {
	return service.getUSDTTRYQuote(ctx, false)
}

func (service *Service) RefreshAll(ctx context.Context, holdings []models.Holding) error {
	_, hasPrecondition := db.PortfolioGeneration(ctx)
	if !holdingsNeedRefresh(holdings) && !hasPrecondition {
		return wrapOperationError("RefreshAll", ctx.Err())
	}

	ctx, err := service.withQuoteGeneration(ctx)
	if err != nil {
		return err
	}

	err = service.refreshHoldings(ctx, holdings)

	return service.recheckClientQuoteGeneration(ctx, err)
}

func (service *Service) TestProvider(
	ctx context.Context,
	providerName, instrumentID string,
) (models.Price, error) {
	return service.testProviderBounded(ctx, providerName, instrumentID)
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
		lastFailure := service.lastErrors[provider.Name]
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
				"SELECT COALESCE(last_error, '') FROM price_cache WHERE provider = ? ORDER BY fetched_at DESC LIMIT 1",
				provider.Name,
			)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, wrapOperationError("GetProviderHealthContext", err)
			}

			errorMsg = lastFailure
			if errorMsg == "" {
				errorMsg = "Provider request failed"
			}
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
		`SELECT id, name, COALESCE(price_provider, '') AS price_provider,
COALESCE(instrument_id, '') AS instrument_id FROM holdings ORDER BY name`,
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

func (service *Service) HasProvider(name string) bool {
	_, ok := service.registry.Get(name)

	return ok
}

var ErrUnknownProvider = errors.New("unknown price provider")

func (service *Service) UpdateHoldingProviderContext(
	ctx context.Context, holdingID int, provider, instrumentID string,
) error {
	if provider != "" && !service.HasProvider(provider) {
		return fmt.Errorf("%w: %s", ErrUnknownProvider, provider)
	}

	result, err := db.ExecPortfolioMutation(ctx, service.db, `
		UPDATE holdings SET price_provider = ?, instrument_id = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`,
		provider,
		instrumentID,
		holdingID,
	)
	if err != nil {
		return wrapOperationError("UpdateHoldingProviderContext", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return wrapOperationError("UpdateHoldingProviderContext", err)
	}

	if affected == 0 {
		return wrapOperationError("UpdateHoldingProviderContext", sql.ErrNoRows)
	}

	return nil
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
	errRefresh               = errors.New("refresh")
	errProvider              = errors.New("provider")
	errCircuitBreakerOpenFor = errors.New("circuit breaker open for")
)

const (
	defaultCacheLifetime    = 24 * time.Hour
	breakerFailureThreshold = 3
)

// ResetFailures clears in-memory provider health after cache/backup replacement.
func (service *Service) ResetFailures() {
	err := service.WithCacheReset(context.Background(), func() error { return nil })
	if err != nil {
		slog.Debug("Skipping closed price-service reset", slog.Any("error", err))
	}
}

func (service *Service) getPrice(
	ctx context.Context,
	providerName, instrumentID string,
	forceRefresh bool,
) (models.Price, error) {
	ctx, err := service.withQuoteGeneration(ctx)
	if err != nil {
		return models.Price{}, err
	}

	quote, err := service.coalescedPrice(ctx, providerName, instrumentID, forceRefresh, false)
	if err != nil {
		err = service.recheckClientQuoteGeneration(ctx, err)
	}

	return quote, err
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

	now := time.Now()
	quote.IsStale = quote.IsStale || !now.Before(quoteCacheExpiry(quote, getter.TTL(), now))
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
COALESCE(last_error, '') AS last_error, id, price, error_count
FROM price_cache WHERE provider = ? AND instrument_id = ?`,
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
	now := time.Now()
	if quote.FetchedAt.IsZero() {
		quote.FetchedAt = now.UTC()
	}

	expiresAt := quoteCacheExpiry(quote, ttl, now)

	result, err := service.db.ExecContext(ctx, `
                INSERT INTO price_cache (provider, instrument_id, price, currency, source,
fetched_at, expires_at, error_count, last_error)
		SELECT ?, ?, ?, ?, ?, ?, ?, 0, ''
 FROM snapshot_state WHERE id = 1 AND (? < 0 OR generation = ?)
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
		quoteGeneration(ctx), quoteGeneration(ctx),
	)
	if err != nil {
		return wrapOperationError("cachePriceContext", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return wrapOperationError("cachePriceContext", err)
	}

	if rows == 0 {
		return errQuoteSuperseded
	}

	return nil
}

func (service *Service) recordFailure(provider, instrumentID string, err error) {
	service.recordFailureContext(context.Background(), provider, instrumentID, err)
}

func (service *Service) recordFailureContext(
	ctx context.Context,
	provider, instrumentID string,
	err error,
) {
	if !service.failureGenerationCurrent(ctx) {
		return
	}

	if !isInstrumentFailure(err) {
		service.mu.Lock()
		if generation := quoteGeneration(ctx); generation >= 0 && generation != service.generation {
			service.mu.Unlock()

			return
		}

		service.failures[provider]++
		service.lastFail[provider] = time.Now()
		service.lastErrors[provider] = safeHealthError(err)

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
	}

	// Database waits must not block other providers' health state.
	_, persistErr := service.db.ExecContext(ctx, `
		UPDATE price_cache SET error_count = error_count + 1, last_error = ?
		WHERE provider = ? AND instrument_id = ?
 AND EXISTS (SELECT 1 FROM snapshot_state WHERE id = 1 AND (? < 0 OR generation = ?))
	`,
		err.Error(),
		provider,
		instrumentID,
		quoteGeneration(ctx), quoteGeneration(ctx),
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

	delete(service.lastErrors, provider)
	service.failures[provider] = 0
	service.breakers[provider] = false
}

func (service *Service) getUSDTTRYQuote(ctx context.Context, force bool) (models.Price, error) {
	primary, primaryErr := service.getPrice(ctx, "Frankfurter", "USD", force)
	if usableFXQuote(&primary, primaryErr) && !primary.IsFailed && !primary.IsStale {
		return primary, nil
	}

	if !force && usableFXQuote(&primary, primaryErr) {
		return service.recoverCachedFX(ctx, &primary)
	}

	fallback, fallbackErr := service.getPrice(ctx, "Yahoo Finance", "USDTRY=X", force)
	if usableFXQuote(&fallback, fallbackErr) && !fallback.IsFailed && !fallback.IsStale {
		return fallback, nil
	}

	if !force {
		if usableFXQuote(&fallback, fallbackErr) {
			return fallback, nil
		}
	}

	return models.Price{}, fmt.Errorf("no fresh FX rate available: %w",
		errors.Join(errRefresh, primaryErr, primary.Error, fallbackErr, fallback.Error))
}

func usableFXQuote(quote *models.Price, err error) bool {
	return err == nil && quote.Value > 0 && !math.IsInf(quote.Value, 0) && !math.IsNaN(quote.Value)
}

func (service *Service) recoverCachedFX(
	ctx context.Context,
	primary *models.Price,
) (models.Price, error) {
	fallback, err := service.backgroundFXQuote(ctx)
	if ctx.Err() != nil {
		return models.Price{}, wrapOperationError("getUSDTTRYQuote", ctx.Err())
	}

	// Prefer a healthy fallback, or the newer cached sample while both refresh.
	if usableFXQuote(&fallback, err) &&
		((!fallback.IsFailed && !fallback.IsStale) || fallback.FetchedAt.After(primary.FetchedAt)) {
		return fallback, nil
	}

	return *primary, nil
}

// Keep serving stale FX immediately, but recover through the same bounded,
// coalesced fetch machinery even when the fallback cache has never been populated.
func (service *Service) backgroundFXQuote(ctx context.Context) (models.Price, error) {
	ctx, err := service.withQuoteGeneration(ctx)
	if err != nil {
		return models.Price{}, err
	}

	return service.coalescedPrice(ctx, "Yahoo Finance", "USDTRY=X", false, true)
}

// Quotes derived from cached metadata must not receive a new freshness window.
func quoteCacheExpiry(quote *models.Price, ttl time.Duration, now time.Time) time.Time {
	if ttl == 0 {
		ttl = defaultCacheLifetime
	}

	fetched := quote.FetchedAt
	if fetched.IsZero() || fetched.After(now) {
		fetched = now
	}

	return fetched.Add(ttl)
}

// Never expose arbitrary provider messages, which can echo credentials or URLs.
func safeHealthError(err error) string {
	switch {
	case errors.Is(err, errAlphaVantageKeyNotSet):
		return "ALPHA_VANTAGE_KEY is not configured"
	case errors.Is(err, errTwelveDataKeyNotSet):
		return "TWELVE_DATA_KEY is not configured"
	case errors.Is(err, errFinnhubKeyNotSet):
		return "FINNHUB_KEY is not configured"
	case errors.Is(err, errRateLimited):
		return "Provider rate limit reached"
	default:
		return "Provider request failed"
	}
}
