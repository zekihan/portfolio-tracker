package models

import (
	"database/sql"
	"time"
)

type AssetType string

const (
	AssetTypeStock  AssetType = "Stock"
	AssetTypeETF    AssetType = "ETF"
	AssetTypeTRFund AssetType = "TR Fund"
	AssetTypeCrypto AssetType = "Crypto"
	AssetTypeGold   AssetType = "Gold"
	AssetTypeCash   AssetType = "Cash"
)

type Holding struct {
	PriceFetchedAt  time.Time    `json:"price_fetched_at,omitzero"  db:"-"`
	UpdatedAt       time.Time    `json:"updated_at"                 db:"updated_at"`
	CreatedAt       time.Time    `json:"created_at"                 db:"created_at"`
	ManualPriceAt   sql.NullTime `json:"manual_price_at"            db:"manual_price_at"`
	PriceProvider   string       `json:"price_provider"             db:"price_provider"`
	CurrentCurrency string       `json:"current_currency,omitempty" db:"-"`
	InstrumentID    string       `json:"instrument_id"              db:"instrument_id"`

	BaseCurrency string `json:"base_currency" db:"base_currency" validate:"oneof=TRY USD EUR"`

	Basket     string `json:"basket"                db:"basket"`
	Location   string `json:"location"              db:"location"`
	Notes      string `json:"notes"                 db:"notes"`
	PriceError string `json:"price_error,omitempty" db:"-"`

	Name string `json:"name" db:"name" validate:"required"`

	PriceSource string `json:"price_source,omitempty" db:"-"`

	AssetType AssetType `json:"asset_type" db:"asset_type" validate:"oneof=Stock ETF 'TR Fund' Crypto Gold Cash"`

	Ticker       string          `json:"ticker"                  db:"ticker"`
	ManualPrice  sql.NullFloat64 `json:"manual_price"            db:"manual_price"`
	CurrentPrice float64         `json:"current_price,omitempty" db:"-"`
	ID           int             `json:"id"                      db:"id"`

	Quantity float64 `json:"quantity" db:"quantity" validate:"gte=0"`

	EstPrice float64 `json:"est_price" db:"est_price" validate:"gte=0"`

	ValuationAvailable bool `json:"valuation_available" db:"-"`
	IsStale            bool `json:"is_stale,omitempty"  db:"-"`
	IsFailed           bool `json:"is_failed,omitempty" db:"-"`
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
	FetchedAt time.Time
	Error     error
	Currency  string
	Source    string
	Value     float64
	IsStale   bool
	IsFailed  bool
}

type Snapshot struct {
	TotalValueEUR *float64  `json:"total_value_eur" db:"total_value_eur"`
	EURTRYRate    *float64  `json:"eur_try_rate"    db:"eur_try_rate"` //nolint:tagliatelle // Acronym should be separated
	Timestamp     time.Time `json:"timestamp"       db:"timestamp"`
	CreatedAt     time.Time `json:"created_at"      db:"created_at"`
	HoldingsJSON  string    `json:"holdings_json"   db:"holdings_json"`
	ID            int       `json:"id"              db:"id"`
	TotalValueTRY float64   `json:"total_value_try" db:"total_value_try"`
	TotalValueUSD float64   `json:"total_value_usd" db:"total_value_usd"`
	USDTRYRate    float64   `json:"usd_try_rate"    db:"usd_try_rate"` //nolint:tagliatelle // Acronym should be separated
	StaleCount    int       `json:"stale_count"     db:"stale_count"`
	FailedCount   int       `json:"failed_count"    db:"failed_count"`
}

type Provider struct {
	UpdatedAt   time.Time `json:"updated_at"            db:"updated_at"`
	LastOK      time.Time `json:"last_ok,omitzero"      db:"-"`
	CreatedAt   time.Time `json:"created_at"            db:"created_at"`
	Name        string    `json:"name"                  db:"name"`
	Type        string    `json:"type"                  db:"type"`
	LastError   string    `json:"last_error,omitempty"  db:"-"`
	KeyEnvVar   string    `json:"key_env_var"           db:"key_env_var"`
	BaseURL     string    `json:"base_url"              db:"base_url"`
	Status      string    `json:"status,omitempty"      db:"-"`
	TTLSeconds  int       `json:"ttl_seconds"           db:"ttl_seconds"`
	ID          int       `json:"id"                    db:"id"`
	ErrorCount  int       `json:"error_count,omitempty" db:"-"`
	Enabled     bool      `json:"enabled"               db:"enabled"`
	KeyRequired bool      `json:"key_required"          db:"key_required"`
}

type PriceCacheEntry struct {
	FetchedAt    time.Time `json:"fetched_at"    db:"fetched_at"`
	ExpiresAt    time.Time `json:"expires_at"    db:"expires_at"`
	Provider     string    `json:"provider"      db:"provider"`
	InstrumentID string    `json:"instrument_id" db:"instrument_id"`
	Currency     string    `json:"currency"      db:"currency"`
	Source       string    `json:"source"        db:"source"`
	LastError    string    `json:"last_error"    db:"last_error"`
	ID           int       `json:"id"            db:"id"`
	Price        float64   `json:"price"         db:"price"`
	ErrorCount   int       `json:"error_count"   db:"error_count"`
}

type Settings struct {
	DisplayCurrency string `json:"display_currency"`
	DarkMode        string `json:"dark_mode"`
	RefreshInterval int    `json:"refresh_interval"`
}

type DashboardData struct {
	FXFetchedAt        time.Time
	LastUpdated        time.Time
	AllocationByBasket map[string]float64
	AllocationByType   map[string]float64
	AllocationByCurr   map[string]float64
	AllocationByLoc    map[string]float64
	ProviderHealth     []ProviderHealth
	CurrentValueTRY    float64
	UnvaluedCount      int
	EstimatedValueEUR  float64
	EstimatedValueUSD  float64
	USDTRYRate         float64
	CurrentValueEUR    float64
	EstimatedValueTRY  float64
	CurrentValueUSD    float64
	ValuedCount        int
	EURTRYRate         float64
	CoverageCount      int
	TotalHoldings      int
	FailedCount        int
	StaleCount         int
	ValuationComplete  bool
	FXIsStale          bool
	FXIsFailed         bool
	EURAvailable       bool
}

type ProviderHealth struct {
	LastOK      time.Time
	Name        string
	Type        string
	Status      string
	ErrorMsg    string
	KeyRequired bool
}

//nolint:govet // Field alignment optimized for readability
type HoldingWithPrice struct {
	Holding

	Price                 Price
	NormPriceEUR          float64
	NormTotalEUR          float64
	NormEstTotalEUR       float64
	EURValuationAvailable bool
	NormPriceTRY          float64
	NormPriceUSD          float64
	NormTotalTRY          float64
	NormTotalUSD          float64
	NormEstTotalTRY       float64
	NormEstTotalUSD       float64
}
