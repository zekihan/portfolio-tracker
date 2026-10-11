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

type Service struct {
	db       *sqlx.DB
	registry *Registry
	failures map[string]int
	lastFail map[string]time.Time
	breakers map[string]bool
	mu       sync.RWMutex
}

func NewService(db *sqlx.DB, registry *Registry) *Service {
	return &Service{
		db:       db,
		registry: registry,
		failures: make(map[string]int),
		lastFail: make(map[string]time.Time),
		breakers: make(map[string]bool),
	}
}

func (s *Service) GetPrice(
	ctx context.Context,
	providerName, instrumentID string,
) (models.Price, error) {
	if ctx.Err() != nil {
		return models.Price{}, ctx.Err()
	}

	getter, ok := s.registry.Get(providerName)
	if !ok {
		return models.Price{}, fmt.Errorf("provider %s not found", providerName)
	}

	// Check cache first.
	cached, err := s.getCachedPriceContext(ctx, providerName, instrumentID)
	if ctx.Err() != nil {
		return models.Price{}, ctx.Err()
	}

	if err == nil && cached != nil && !cached.ExpiresAt.Before(time.Now()) {
		price := models.Price{
			Value:     cached.Price,
			Currency:  cached.Currency,
			Source:    cached.Source,
			FetchedAt: cached.FetchedAt,
			IsStale:   false,
		}

		return price, nil
	}

	// Check circuit breaker.
	s.mu.RLock()
	breakerOpen := s.breakers[providerName]
	s.mu.RUnlock()

	if breakerOpen {
		// Return stale cached data if available.
		if cached != nil {
			price := models.Price{
				Value:     cached.Price,
				Currency:  cached.Currency,
				Source:    cached.Source,
				FetchedAt: cached.FetchedAt,
				IsStale:   true,
				IsFailed:  true,
			}

			return price, nil
		}

		return models.Price{}, fmt.Errorf("circuit breaker open for %s", providerName)
	}

	// Fetch fresh price.
	price, err := getter.GetPrice(ctx, instrumentID)
	if ctx.Err() != nil {
		return models.Price{}, ctx.Err()
	}

	if err != nil {
		s.recordFailureContext(ctx, providerName, err)

		if ctx.Err() != nil {
			return models.Price{}, ctx.Err()
		}
		// Return stale cached data if available.
		if cached != nil {
			price = models.Price{
				Value:     cached.Price,
				Currency:  cached.Currency,
				Source:    cached.Source,
				FetchedAt: cached.FetchedAt,
				IsStale:   true,
				IsFailed:  true,
			}

			return price, nil
		}

		return models.Price{}, err
	}

	// Cache before recording success so expired requests cannot reset health.
	s.cachePriceContext(ctx, providerName, instrumentID, price, getter.TTL())

	if ctx.Err() != nil {
		return models.Price{}, ctx.Err()
	}

	s.recordSuccess(providerName)

	return price, nil
}

func (s *Service) GetUSDTTRYRate(ctx context.Context) (float64, error) {
	// Try Frankfurter first.
	price, err := s.GetPrice(ctx, "Frankfurter", "USD")
	if err == nil && price.Value > 0 && !math.IsInf(price.Value, 0) {
		return price.Value, nil
	}
	// Fallback to Yahoo Finance FX.
	price, err = s.GetPrice(ctx, "Yahoo Finance", "USDTRY=X")
	if err == nil {
		if price.Value > 0 && !math.IsInf(price.Value, 0) {
			return price.Value, nil
		}

		return 0, fmt.Errorf("invalid FX rate from Yahoo Finance: %v", price.Value)
	}

	return 0, fmt.Errorf("no FX rate available: %w", err)
}

func (s *Service) RefreshAll(ctx context.Context, holdings []models.Holding) error {
	var refreshErrors []error

	for _, h := range holdings {
		if h.PriceProvider == "" || h.PriceProvider == "Manual" {
			continue
		}

		if h.InstrumentID == "" {
			continue
		}

		fetched, err := s.GetPrice(ctx, h.PriceProvider, h.InstrumentID)
		if err != nil {
			refreshErrors = append(refreshErrors,
				fmt.Errorf("refresh %s/%s: %w", h.PriceProvider, h.InstrumentID, err))
		} else if fetched.IsFailed {
			refreshErrors = append(refreshErrors,
				fmt.Errorf("refresh %s/%s failed: using stale cached data",
					h.PriceProvider, h.InstrumentID))
		}
	}

	return errors.Join(refreshErrors...)
}

func (s *Service) TestProvider(
	ctx context.Context,
	providerName, instrumentID string,
) (models.Price, error) {
	if ctx.Err() != nil {
		return models.Price{}, ctx.Err()
	}

	getter, ok := s.registry.Get(providerName)
	if !ok {
		return models.Price{}, fmt.Errorf("provider %s not found", providerName)
	}

	quote, err := getter.GetPrice(ctx, instrumentID)
	if ctx.Err() != nil {
		return models.Price{}, ctx.Err()
	}

	return quote, err
}

func (s *Service) getCachedPrice(provider, instrumentID string) (*models.PriceCacheEntry, error) {
	return s.getCachedPriceContext(context.Background(), provider, instrumentID)
}

func (s *Service) getCachedPriceContext(
	ctx context.Context, provider, instrumentID string,
) (*models.PriceCacheEntry, error) {
	var entry models.PriceCacheEntry

	err := s.db.GetContext(ctx, &entry, `
		SELECT * FROM price_cache WHERE provider = ? AND instrument_id = ?
	`, provider, instrumentID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	return &entry, nil
}

func (s *Service) cachePrice(
	provider, instrumentID string,
	price models.Price,
	ttl time.Duration,
) error {
	return s.cachePriceContext(context.Background(), provider, instrumentID, price, ttl)
}

func (s *Service) cachePriceContext(
	ctx context.Context, provider, instrumentID string,
	price models.Price, ttl time.Duration,
) error {
	expiresAt := time.Now().Add(ttl)
	if ttl == 0 {
		expiresAt = time.Now().Add(24 * time.Hour)
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO price_cache (provider, instrument_id, price, currency, source, fetched_at, expires_at, error_count, last_error)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, '')
		ON CONFLICT(provider, instrument_id) DO UPDATE SET
			price = excluded.price,
			currency = excluded.currency,
			source = excluded.source,
			fetched_at = excluded.fetched_at,
			expires_at = excluded.expires_at,
			error_count = 0,
			last_error = ''
	`, provider, instrumentID, price.Value, price.Currency, price.Source, price.FetchedAt.UTC().Format(time.RFC3339), expiresAt.UTC().Format(time.RFC3339))

	return err
}

func (s *Service) recordFailure(provider string, err error) {
	s.recordFailureContext(context.Background(), provider, err)
}

func (s *Service) recordFailureContext(ctx context.Context, provider string, err error) {
	s.mu.Lock()
	s.failures[provider]++
	s.lastFail[provider] = time.Now()

	// Open circuit breaker after 3 failures.
	if s.failures[provider] >= 3 {
		s.breakers[provider] = true
		slog.Warn("circuit breaker opened", "provider", provider, "failures", s.failures[provider])
	}

	s.mu.Unlock()

	// Database waits must not block other providers' health state.
	_, _ = s.db.ExecContext(ctx, `
		UPDATE price_cache SET error_count = error_count + 1, last_error = ?
		WHERE provider = ?
	`, err.Error(), provider)
}

func (s *Service) recordSuccess(provider string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.failures[provider] = 0
	s.breakers[provider] = false
}

func (s *Service) GetProviderHealth() ([]models.ProviderHealth, error) {
	var providers []models.Provider

	err := s.db.Select(
		&providers,
		"SELECT * FROM provider_registry WHERE enabled = 1 ORDER BY name",
	)
	if err != nil {
		return nil, err
	}

	health := make([]models.ProviderHealth, 0, len(providers))
	for _, p := range providers {
		s.mu.RLock()
		failures := s.failures[p.Name]
		breaker := s.breakers[p.Name]
		s.mu.RUnlock()

		// Get last successful fetch from cache.
		var lastOK sql.NullTime

		err = s.db.Get(&lastOK, `
			SELECT fetched_at FROM price_cache
			WHERE provider = ? AND error_count = 0
			ORDER BY fetched_at DESC LIMIT 1
		`, p.Name)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
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

			err = s.db.Get(
				&lastErr,
				"SELECT last_error FROM price_cache WHERE provider = ? ORDER BY fetched_at DESC LIMIT 1",
				p.Name,
			)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}

			errorMsg = lastErr
		}

		health = append(health, models.ProviderHealth{
			Name:        p.Name,
			Type:        p.Type,
			KeyRequired: p.KeyRequired,
			Status:      status,
			LastOK:      lastOK.Time,
			ErrorMsg:    errorMsg,
		})
	}

	return health, nil
}

func (s *Service) GetHoldingProviderMapping() ([]HoldingProviderMapping, error) {
	var holdings []models.Holding

	err := s.db.Select(
		&holdings,
		"SELECT id, name, price_provider, instrument_id FROM holdings ORDER BY name",
	)
	if err != nil {
		return nil, err
	}

	mappings := make([]HoldingProviderMapping, 0, len(holdings))
	for _, h := range holdings {
		mappings = append(mappings, HoldingProviderMapping{
			HoldingID:    h.ID,
			HoldingName:  h.Name,
			Provider:     h.PriceProvider,
			InstrumentID: h.InstrumentID,
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

func (s *Service) UpdateHoldingProvider(holdingID int, provider, instrumentID string) error {
	return s.UpdateHoldingProviderContext(context.Background(), holdingID, provider, instrumentID)
}

func (s *Service) UpdateHoldingProviderContext(
	ctx context.Context, holdingID int, provider, instrumentID string,
) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE holdings SET price_provider = ?, instrument_id = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, provider, instrumentID, holdingID)

	return err
}

func (s *Service) ClearCache() error {
	_, err := s.db.Exec("DELETE FROM price_cache")

	return err
}

func (s *Service) GetProvidersJSON() (string, error) {
	providers, err := s.registry.GetProvidersJSON()
	if err != nil {
		return "", err
	}

	data, _ := json.Marshal(providers)

	return string(data), nil
}

func (r *Registry) GetProvidersJSON() ([]map[string]any, error) {
	result := make([]map[string]any, 0)
	for _, g := range r.All() {
		result = append(result, map[string]any{
			"name":        g.Name(),
			"type":        g.Type(),
			"ttl_seconds": int(g.TTL().Seconds()),
		})
	}

	return result, nil
}
