package price

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

type Getter interface {
	GetPrice(ctx context.Context, instrumentID string) (models.Price, error)
	Name() string
	Type() string
	TTL() time.Duration
}

type Registry struct {
	getters map[string]Getter
}

func NewRegistry() *Registry {
	return &Registry{getters: make(map[string]Getter)}
}

func (r *Registry) Register(g Getter) {
	r.getters[g.Name()] = g
}

func (r *Registry) Get(name string) (Getter, bool) {
	g, ok := r.getters[name]

	return g, ok
}

func (r *Registry) All() []Getter {
	result := make([]Getter, 0, len(r.getters))
	for _, g := range r.getters {
		result = append(result, g)
	}

	return result
}

func (r *Registry) GetByType(providerType string) []Getter {
	var result []Getter

	for _, g := range r.getters {
		if g.Type() == providerType {
			result = append(result, g)
		}
	}

	return result
}

func DefaultRegistry() *Registry {
	r := NewRegistry()

	// Register all providers.
	r.Register(NewYahooFinance())
	r.Register(NewAlphaVantage())
	r.Register(NewTwelveData())
	r.Register(NewFinnhub())
	r.Register(NewPusulaPortfoy())
	r.Register(NewGarantiBBVA())
	r.Register(NewIZKOGold())
	r.Register(NewTradingViewBIST())
	r.Register(NewTradingViewCrypto())
	r.Register(NewCoinGecko())
	r.Register(NewFrankfurter())
	r.Register(NewManual())

	return r
}

type baseGetter struct {
	name         string
	providerType string
	ttl          time.Duration
	client       *http.Client
}

func (b *baseGetter) Name() string       { return b.name }
func (b *baseGetter) Type() string       { return b.providerType }
func (b *baseGetter) TTL() time.Duration { return b.ttl }

type YahooFinance struct{ baseGetter }

func NewYahooFinance() *YahooFinance {
	return &YahooFinance{
		baseGetter{
			name:         "Yahoo Finance",
			providerType: "Stock/ETF",
			ttl:          60 * time.Second,
			client:       &http.Client{Timeout: 10 * time.Second},
		},
	}
}

func (y *YahooFinance) GetPrice(ctx context.Context, instrumentID string) (models.Price, error) {
	url := fmt.Sprintf(
		"https://query1.finance.yahoo.com/v8/finance/chart/%s?interval=1m&range=1d",
		url.QueryEscape(instrumentID),
	)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := y.client.Do(req)
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return models.Price{}, fmt.Errorf("yahoo finance returned HTTP %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Chart struct {
			Result []struct {
				Meta struct {
					RegularMarketPrice float64 `json:"regularMarketPrice"`
					Currency           string  `json:"currency"`
				} `json:"meta"`
			} `json:"result"`
			Error any `json:"error"`
		} `json:"chart"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.Price{}, err
	}

	if len(result.Chart.Result) == 0 {
		return models.Price{}, fmt.Errorf("no data for %s", instrumentID)
	}

	price := result.Chart.Result[0].Meta.RegularMarketPrice
	if price <= 0 {
		return models.Price{}, fmt.Errorf("invalid market price for %s", instrumentID)
	}

	currency := result.Chart.Result[0].Meta.Currency
	if currency == "" {
		currency = "USD"
	}

	return models.Price{
		Value:     price,
		Currency:  currency,
		Source:    y.name,
		FetchedAt: time.Now().UTC(),
	}, nil
}

type AlphaVantage struct{ baseGetter }

func NewAlphaVantage() *AlphaVantage {
	return &AlphaVantage{
		baseGetter{
			name:         "Alpha Vantage",
			providerType: "Stock/ETF",
			ttl:          60 * time.Second,
			client:       &http.Client{Timeout: 10 * time.Second},
		},
	}
}

func (a *AlphaVantage) GetPrice(ctx context.Context, instrumentID string) (models.Price, error) {
	key := os.Getenv("ALPHA_VANTAGE_KEY")
	if key == "" {
		return models.Price{}, errors.New("ALPHA_VANTAGE_KEY not set")
	}

	url := fmt.Sprintf(
		"https://www.alphavantage.co/query?function=GLOBAL_QUOTE&symbol=%s&apikey=%s",
		url.QueryEscape(instrumentID),
		key,
	)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)

	resp, err := a.client.Do(req)
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		GlobalQuote struct {
			Price    string `json:"05. price"`
			Currency string `json:"08. currency"`
		} `json:"Global Quote"`
		Note string `json:"Note"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.Price{}, err
	}

	if result.Note != "" {
		return models.Price{}, fmt.Errorf("rate limited: %s", result.Note)
	}

	price := 0.0
	fmt.Sscanf(result.GlobalQuote.Price, "%f", &price)

	currency := result.GlobalQuote.Currency
	if currency == "" {
		currency = "USD"
	}

	if price == 0 {
		return models.Price{}, fmt.Errorf("no price data for %s", instrumentID)
	}

	return models.Price{
		Value:     price,
		Currency:  currency,
		Source:    a.name,
		FetchedAt: time.Now().UTC(),
	}, nil
}

type TwelveData struct{ baseGetter }

func NewTwelveData() *TwelveData {
	return &TwelveData{
		baseGetter{
			name:         "Twelve Data",
			providerType: "Stock/ETF",
			ttl:          60 * time.Second,
			client:       &http.Client{Timeout: 10 * time.Second},
		},
	}
}

func (t *TwelveData) GetPrice(ctx context.Context, instrumentID string) (models.Price, error) {
	key := os.Getenv("TWELVE_DATA_KEY")
	if key == "" {
		return models.Price{}, errors.New("TWELVE_DATA_KEY not set")
	}

	url := fmt.Sprintf(
		"https://api.twelvedata.com/price?symbol=%s&apikey=%s",
		url.QueryEscape(instrumentID),
		key,
	)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)

	resp, err := t.client.Do(req)
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Price    string `json:"price"`
		Currency string `json:"currency"`
		Code     int    `json:"code"`
		Message  string `json:"message"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.Price{}, err
	}

	if result.Code != 0 {
		return models.Price{}, fmt.Errorf("API error: %s", result.Message)
	}

	price := 0.0
	fmt.Sscanf(result.Price, "%f", &price)

	currency := result.Currency
	if currency == "" {
		currency = "USD"
	}

	return models.Price{
		Value:     price,
		Currency:  currency,
		Source:    t.name,
		FetchedAt: time.Now().UTC(),
	}, nil
}

type Finnhub struct{ baseGetter }

func NewFinnhub() *Finnhub {
	return &Finnhub{
		baseGetter{
			name:         "Finnhub",
			providerType: "Stock/ETF",
			ttl:          60 * time.Second,
			client:       &http.Client{Timeout: 10 * time.Second},
		},
	}
}

func (f *Finnhub) GetPrice(ctx context.Context, instrumentID string) (models.Price, error) {
	key := os.Getenv("FINNHUB_KEY")
	if key == "" {
		return models.Price{}, errors.New("FINNHUB_KEY not set")
	}

	url := fmt.Sprintf(
		"https://finnhub.io/api/v1/quote?symbol=%s&token=%s",
		url.QueryEscape(instrumentID),
		key,
	)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)

	resp, err := f.client.Do(req)
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		C float64 `json:"c"` // Current price
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.Price{}, err
	}

	if result.C == 0 {
		return models.Price{}, fmt.Errorf("no price data for %s", instrumentID)
	}

	return models.Price{
		Value:     result.C,
		Currency:  "USD",
		Source:    f.name,
		FetchedAt: time.Now().UTC(),
	}, nil
}

type PusulaPortfoy struct{ baseGetter }

func NewPusulaPortfoy() *PusulaPortfoy {
	return &PusulaPortfoy{
		baseGetter{
			name:         "Pusula Portföy",
			providerType: "TR Fund",
			ttl:          60 * time.Second,
			client:       &http.Client{Timeout: 10 * time.Second},
		},
	}
}

func (p *PusulaPortfoy) GetPrice(ctx context.Context, instrumentID string) (models.Price, error) {
	url := "https://www.pusulaportfoy.com.tr/api/fund/" + url.QueryEscape(instrumentID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)

	resp, err := p.client.Do(req)
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Data struct {
			Price float64 `json:"price"`
		} `json:"data"`
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.Price{}, err
	}

	if !result.Success || result.Data.Price == 0 {
		return models.Price{}, fmt.Errorf("no price data for %s", instrumentID)
	}

	return models.Price{
		Value:     result.Data.Price,
		Currency:  "TRY",
		Source:    p.name,
		FetchedAt: time.Now().UTC(),
	}, nil
}

type GarantiBBVA struct{ baseGetter }

func NewGarantiBBVA() *GarantiBBVA {
	return &GarantiBBVA{
		baseGetter{
			name:         "Garanti BBVA",
			providerType: "TR Fund",
			ttl:          60 * time.Second,
			client:       &http.Client{Timeout: 10 * time.Second},
		},
	}
}

func (g *GarantiBBVA) GetPrice(ctx context.Context, instrumentID string) (models.Price, error) {
	url := "https://www.garantibbva.com.tr/api/fund/" + url.QueryEscape(instrumentID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)

	resp, err := g.client.Do(req)
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Price float64 `json:"price"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.Price{}, err
	}

	if result.Price == 0 {
		return models.Price{}, fmt.Errorf("no price data for %s", instrumentID)
	}

	return models.Price{
		Value:     result.Price,
		Currency:  "TRY",
		Source:    g.name,
		FetchedAt: time.Now().UTC(),
	}, nil
}

type IZKOGold struct{ baseGetter }

func NewIZKOGold() *IZKOGold {
	return &IZKOGold{
		baseGetter{
			name:         "İZKO Gold",
			providerType: "Gold",
			ttl:          60 * time.Second,
			client:       &http.Client{Timeout: 10 * time.Second},
		},
	}
}

func (i *IZKOGold) GetPrice(ctx context.Context, instrumentID string) (models.Price, error) {
	url := "https://www.izko.com.tr/api/gold-prices"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)

	resp, err := i.client.Do(req)
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result []struct {
		Name  string  `json:"name"`
		Price float64 `json:"price"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.Price{}, err
	}

	for _, item := range result {
		if strings.EqualFold(item.Name, instrumentID) {
			return models.Price{
				Value:     item.Price,
				Currency:  "TRY",
				Source:    i.name,
				FetchedAt: time.Now().UTC(),
			}, nil
		}
	}

	return models.Price{}, fmt.Errorf("instrument %s not found", instrumentID)
}

type TradingViewBIST struct{ baseGetter }

func NewTradingViewBIST() *TradingViewBIST {
	return &TradingViewBIST{
		baseGetter{
			name:         "TradingView BIST",
			providerType: "Gold",
			ttl:          60 * time.Second,
			client:       &http.Client{Timeout: 10 * time.Second},
		},
	}
}

func (t *TradingViewBIST) GetPrice(ctx context.Context, instrumentID string) (models.Price, error) {
	url := fmt.Sprintf(
		"https://symbol-search.tradingview.com/symbol_search/?text=%s&type=stock",
		url.QueryEscape(instrumentID),
	)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)

	resp, err := t.client.Do(req)
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result []struct {
		Symbol string  `json:"symbol"`
		Price  float64 `json:"price"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.Price{}, err
	}

	for _, item := range result {
		if strings.EqualFold(item.Symbol, instrumentID) {
			return models.Price{
				Value:     item.Price,
				Currency:  "TRY",
				Source:    t.name,
				FetchedAt: time.Now().UTC(),
			}, nil
		}
	}

	return models.Price{}, fmt.Errorf("instrument %s not found", instrumentID)
}

type TradingViewCrypto struct{ baseGetter }

func NewTradingViewCrypto() *TradingViewCrypto {
	return &TradingViewCrypto{
		baseGetter{
			name:         "TradingView Crypto",
			providerType: "Crypto",
			ttl:          60 * time.Second,
			client:       &http.Client{Timeout: 10 * time.Second},
		},
	}
}

func (t *TradingViewCrypto) GetPrice(
	ctx context.Context,
	instrumentID string,
) (models.Price, error) {
	url := fmt.Sprintf(
		"https://symbol-search.tradingview.com/symbol_search/?text=%s&type=crypto",
		url.QueryEscape(instrumentID),
	)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)

	resp, err := t.client.Do(req)
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result []struct {
		Symbol string  `json:"symbol"`
		Price  float64 `json:"price"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.Price{}, err
	}

	for _, item := range result {
		if strings.EqualFold(item.Symbol, instrumentID) {
			return models.Price{
				Value:     item.Price,
				Currency:  "USD",
				Source:    t.name,
				FetchedAt: time.Now().UTC(),
			}, nil
		}
	}

	return models.Price{}, fmt.Errorf("instrument %s not found", instrumentID)
}

type CoinGecko struct{ baseGetter }

func NewCoinGecko() *CoinGecko {
	return &CoinGecko{
		baseGetter{
			name:         "CoinGecko",
			providerType: "Crypto",
			ttl:          60 * time.Second,
			client:       &http.Client{Timeout: 10 * time.Second},
		},
	}
}

func (c *CoinGecko) GetPrice(ctx context.Context, instrumentID string) (models.Price, error) {
	url := fmt.Sprintf(
		"https://api.coingecko.com/api/v3/simple/price?ids=%s&vs_currencies=usd",
		url.QueryEscape(instrumentID),
	)

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if key := os.Getenv("COINGECKO_KEY"); key != "" {
		req.Header.Set("X-Cg-Demo-Api-Key", key)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result map[string]struct {
		USD float64 `json:"usd"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.Price{}, err
	}

	if data, ok := result[instrumentID]; ok {
		return models.Price{
			Value:     data.USD,
			Currency:  "USD",
			Source:    c.name,
			FetchedAt: time.Now().UTC(),
		}, nil
	}

	return models.Price{}, fmt.Errorf("instrument %s not found", instrumentID)
}

type Frankfurter struct{ baseGetter }

func NewFrankfurter() *Frankfurter {
	return &Frankfurter{
		baseGetter{
			name:         "Frankfurter",
			providerType: "FX",
			ttl:          60 * time.Second,
			client:       &http.Client{Timeout: 10 * time.Second},
		},
	}
}

func (f *Frankfurter) GetPrice(ctx context.Context, instrumentID string) (models.Price, error) {
	url := fmt.Sprintf(
		"https://api.frankfurter.dev/v1/latest?from=%s&to=TRY",
		url.QueryEscape(instrumentID),
	)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)

	resp, err := f.client.Do(req)
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Rates map[string]float64 `json:"rates"`
		Date  string             `json:"date"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.Price{}, err
	}

	if rate, ok := result.Rates["TRY"]; ok {
		return models.Price{
			Value:     rate,
			Currency:  "TRY",
			Source:    f.name,
			FetchedAt: time.Now().UTC(),
		}, nil
	}

	return models.Price{}, fmt.Errorf("no rate for %s", instrumentID)
}

type Manual struct{ baseGetter }

func NewManual() *Manual {
	return &Manual{
		baseGetter{
			name:         "Manual",
			providerType: "Manual",
			ttl:          0,
			client:       nil,
		},
	}
}

func (m *Manual) GetPrice(ctx context.Context, instrumentID string) (models.Price, error) {
	price := 0.0
	fmt.Sscanf(instrumentID, "%f", &price)

	return models.Price{
		Value:     price,
		Currency:  "TRY",
		Source:    m.name,
		FetchedAt: time.Now().UTC(),
	}, nil
}
