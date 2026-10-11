package models

import (
	"database/sql"
	"time"
)

type AssetType string

const (
	AssetTypeStock    AssetType = "Stock"
	AssetTypeETF      AssetType = "ETF"
	AssetTypeTRFund   AssetType = "TR Fund"
	AssetTypeCrypto   AssetType = "Crypto"
	AssetTypeGold     AssetType = "Gold"
	AssetTypeCash     AssetType = "Cash"
)

type Holding struct {
	ID              int       `db:"id" json:"id"`
	Name            string    `db:"name" json:"name"`
	Ticker          string    `db:"ticker" json:"ticker"`
	AssetType       AssetType `db:"asset_type" json:"asset_type"`
	Quantity        float64   `db:"quantity" json:"quantity"`
	PriceProvider   string    `db:"price_provider" json:"price_provider"`
	InstrumentID    string    `db:"instrument_id" json:"instrument_id"`
	BaseCurrency    string    `db:"base_currency" json:"base_currency"`
	Basket          string    `db:"basket" json:"basket"`
	Location        string    `db:"location" json:"location"`
	Notes           string         `db:"notes" json:"notes"`
	EstPrice        float64        `db:"est_price" json:"est_price"`
	ManualPrice     sql.NullFloat64 `db:"manual_price" json:"manual_price"`
	ManualPriceAt   sql.NullTime   `db:"manual_price_at" json:"manual_price_at"`
	CreatedAt       time.Time `db:"created_at" json:"created_at"`
	UpdatedAt       time.Time `db:"updated_at" json:"updated_at"`

	// Computed fields (not in DB)
	CurrentPrice    float64   `db:"-" json:"current_price,omitempty"`
	CurrentCurrency string    `db:"-" json:"current_currency,omitempty"`
	PriceSource     string    `db:"-" json:"price_source,omitempty"`
	PriceFetchedAt  time.Time `db:"-" json:"price_fetched_at,omitempty"`
	PriceError      string    `db:"-" json:"price_error,omitempty"`
	IsStale         bool      `db:"-" json:"is_stale,omitempty"`
	IsFailed        bool      `db:"-" json:"is_failed,omitempty"`
}

func (h *Holding) TotalValue() float64 {
	price := h.CurrentPrice
	if h.ManualPrice.Valid && h.ManualPrice.Float64 > 0 {
		price = h.ManualPrice.Float64
	}
	return h.Quantity * price
}

func (h *Holding) EstimatedTotalValue() float64 {
	if h.EstPrice <= 0 {
		return 0
	}
	return h.Quantity * h.EstPrice
}

type Price struct {
	Value      float64
	Currency   string
	Source     string
	FetchedAt  time.Time
	Error      error
	IsStale    bool
	IsFailed   bool
}

type Snapshot struct {
	ID            int       `db:"id" json:"id"`
	Timestamp     time.Time `db:"timestamp" json:"timestamp"`
	TotalValueTRY float64   `db:"total_value_try" json:"total_value_try"`
	TotalValueUSD float64   `db:"total_value_usd" json:"total_value_usd"`
	USDTRYRate    float64   `db:"usd_try_rate" json:"usd_try_rate"`
	StaleCount    int       `db:"stale_count" json:"stale_count"`
	FailedCount   int       `db:"failed_count" json:"failed_count"`
	HoldingsJSON  string    `db:"holdings_json" json:"holdings_json"`
	CreatedAt     time.Time `db:"created_at" json:"created_at"`
}

type Provider struct {
	ID          int       `db:"id" json:"id"`
	Name        string    `db:"name" json:"name"`
	Type        string    `db:"type" json:"type"`
	KeyRequired bool      `db:"key_required" json:"key_required"`
	KeyEnvVar   string    `db:"key_env_var" json:"key_env_var"`
	BaseURL     string    `db:"base_url" json:"base_url"`
	TTLSeconds  int       `db:"ttl_seconds" json:"ttl_seconds"`
	Enabled     bool      `db:"enabled" json:"enabled"`
	CreatedAt   time.Time `db:"created_at" json:"created_at"`
	UpdatedAt   time.Time `db:"updated_at" json:"updated_at"`

	// Runtime status
	Status      string    `db:"-" json:"status,omitempty"`
	LastOK      time.Time `db:"-" json:"last_ok,omitempty"`
	ErrorCount  int       `db:"-" json:"error_count,omitempty"`
	LastError   string    `db:"-" json:"last_error,omitempty"`
}

type PriceCacheEntry struct {
	ID           int       `db:"id" json:"id"`
	Provider     string    `db:"provider" json:"provider"`
	InstrumentID string    `db:"instrument_id" json:"instrument_id"`
	Price        float64   `db:"price" json:"price"`
	Currency     string    `db:"currency" json:"currency"`
	Source       string    `db:"source" json:"source"`
	FetchedAt    time.Time `db:"fetched_at" json:"fetched_at"`
	ExpiresAt    time.Time `db:"expires_at" json:"expires_at"`
	ErrorCount   int       `db:"error_count" json:"error_count"`
	LastError    string    `db:"last_error" json:"last_error"`
}

type Settings struct {
	DisplayCurrency string `json:"display_currency"`
	DarkMode        string `json:"dark_mode"`
	RefreshInterval int    `json:"refresh_interval"`
}

type DashboardData struct {
	CurrentValueTRY    float64
	CurrentValueUSD    float64
	EstimatedValueTRY  float64
	EstimatedValueUSD  float64
	USDTRYRate         float64
	StaleCount         int
	FailedCount        int
	CoverageCount      int
	TotalHoldings      int
	LastUpdated        time.Time
	AllocationByType   map[string]float64
	AllocationByBasket map[string]float64
	AllocationByLoc    map[string]float64
	AllocationByCurr   map[string]float64
	ProviderHealth     []ProviderHealth
}

type ProviderHealth struct {
	Name       string
	Type       string
	KeyRequired bool
	Status     string
	LastOK     time.Time
	ErrorMsg   string
}

type HoldingWithPrice struct {
	Holding
	Price    Price
	NormPriceTRY float64
	NormPriceUSD float64
	NormTotalTRY float64
	NormTotalUSD float64
	NormEstTotalTRY float64
	NormEstTotalUSD float64
}