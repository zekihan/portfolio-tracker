package price

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

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

	slices.SortFunc(result, func(a, b Getter) int {
		return strings.Compare(a.Name(), b.Name())
	})

	return result
}

func (r *Registry) GetByType(providerType string) []Getter {
	var result []Getter

	for _, g := range r.All() {
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

// doGet issues a GET request with the given headers. Request construction
// errors are returned instead of being discarded, so a bad URL cannot lead to
// a nil request being dereferenced.
func doGet(
	ctx context.Context,
	client *http.Client,
	target string,
	headers map[string]string,
) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", redactURLError(err))
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, redactURLError(err)
	}

	return resp, nil
}

// redactURLError drops the request URL from *url.Error values. Provider URLs
// can carry API keys in their query strings, and these messages are stored in
// price_cache.last_error and returned by the sources test endpoint.
// The wrapped cause is kept so errors.Is checks such as context.Canceled still work.
func redactURLError(err error) error {
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		return fmt.Errorf("%s request failed: %w", urlErr.Op, urlErr.Err)
	}

	return err
}

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
	instrumentID = strings.TrimSpace(instrumentID)
	for _, prefix := range []string{"NASDAQ:", "NYSE:", "NYSEARCA:", "AMEX:"} {
		if strings.HasPrefix(strings.ToUpper(instrumentID), prefix) {
			instrumentID = instrumentID[len(prefix):]

			break
		}
	}

	url := fmt.Sprintf(
		"https://query1.finance.yahoo.com/v8/finance/chart/%s?interval=1m&range=1d",
		url.PathEscape(instrumentID),
	)

	headers := map[string]string{"User-Agent": "Mozilla/5.0"}

	resp, err := doGet(ctx, y.client, url, headers)
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return models.Price{}, fmt.Errorf("yahoo finance returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return models.Price{}, fmt.Errorf("read yahoo finance response: %w", err)
	}

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

	if result.Chart.Error != nil {
		return models.Price{}, fmt.Errorf("yahoo finance API error for %s", instrumentID)
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
		url.QueryEscape(key),
	)

	resp, err := doGet(ctx, a.client, url, nil)
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return models.Price{}, fmt.Errorf("alpha vantage returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return models.Price{}, fmt.Errorf("read provider response: %w", err)
	}

	var result struct {
		GlobalQuote struct {
			Price    string `json:"05. price"`
			Currency string `json:"08. currency"`
		} `json:"Global Quote"`
		Note         string `json:"Note"`
		ErrorMessage string `json:"Error Message"`
		Information  string `json:"Information"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.Price{}, err
	}

	if result.ErrorMessage != "" || result.Information != "" {
		return models.Price{}, fmt.Errorf("alpha vantage API error for %s", instrumentID)
	}

	if result.Note != "" {
		return models.Price{}, fmt.Errorf("rate limited: %s", result.Note)
	}

	price, err := strconv.ParseFloat(result.GlobalQuote.Price, 64)
	if err != nil {
		return models.Price{}, fmt.Errorf("parse alpha vantage price: %w", err)
	}

	if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return models.Price{}, fmt.Errorf("invalid alpha vantage price for %s", instrumentID)
	}

	currency := result.GlobalQuote.Currency
	if currency == "" {
		currency = "USD"
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
		url.QueryEscape(key),
	)

	resp, err := doGet(ctx, t.client, url, nil)
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return models.Price{}, fmt.Errorf("twelve data returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return models.Price{}, fmt.Errorf("read provider response: %w", err)
	}

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

	price, err := strconv.ParseFloat(result.Price, 64)
	if err != nil {
		return models.Price{}, fmt.Errorf("parse twelve data price: %w", err)
	}

	if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return models.Price{}, fmt.Errorf("invalid twelve data price for %s", instrumentID)
	}

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

	url := "https://finnhub.io/api/v1/quote?symbol=" + url.QueryEscape(instrumentID)

	resp, err := doGet(ctx, f.client, url, map[string]string{"X-Finnhub-Token": key})
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return models.Price{}, fmt.Errorf("finnhub returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return models.Price{}, fmt.Errorf("read provider response: %w", err)
	}

	var result struct {
		C float64 `json:"c"` // Current price
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.Price{}, err
	}

	if result.C <= 0 {
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
	body, err := providerRequest(
		ctx,
		p.client,
		"https://www.pusulaportfoy.com.tr/api/funds?pageSize=200&active=true",
		nil,
	)
	if err != nil {
		return models.Price{}, err
	}

	var result struct {
		Items []struct {
			Code  string `json:"abbreviation"`
			Price string `json:"fundPrice"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.Price{}, err
	}

	for _, fund := range result.Items {
		if strings.EqualFold(fund.Code, strings.TrimSpace(instrumentID)) {
			value, err := strconv.ParseFloat(fund.Price, 64)
			if err != nil {
				return models.Price{}, fmt.Errorf("invalid fund price: %w", err)
			}

			return checkedQuote(value, "TRY", p.name)
		}
	}

	return models.Price{}, fmt.Errorf("fund %s not found", instrumentID)
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
	code := strings.ToUpper(strings.TrimSpace(instrumentID))

	const base = "https://www.garantibbvaportfoy.com.tr/"

	body, err := providerRequest(ctx, g.client, base+"webservice/lastdate", map[string]string{})
	if err != nil {
		return models.Price{}, err
	}

	var date string
	if err := json.Unmarshal(body, &date); err != nil {
		return models.Price{}, err
	}

	if _, err := time.Parse(time.DateOnly, date); err != nil {
		return models.Price{}, fmt.Errorf("invalid fund date: %w", err)
	}

	body, err = providerRequest(
		ctx,
		g.client,
		base+"webservice/funddailyvalues",
		map[string]string{"lang": "tr", "code": code, "date": date, "rollBack": "T"},
	)
	if err != nil {
		return models.Price{}, err
	}

	var nested string
	if json.Unmarshal(body, &nested) == nil {
		body = []byte(nested)
	}

	var result struct {
		Data struct {
			Close float64 `json:"Close"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.Price{}, err
	}
	// The quote endpoint omits currency; use the provider's fund metadata.
	body, err = providerRequest(ctx, g.client, base+"fon-getirileri", nil)
	if err != nil {
		return models.Price{}, err
	}

	currency, err := garantiCurrency(body, code)
	if err != nil {
		return models.Price{}, err
	}

	return checkedQuote(result.Data.Close, currency, g.name)
}

func garantiCurrency(body []byte, code string) (string, error) {
	tokenizer := html.NewTokenizer(bytes.NewReader(body))
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return "", fmt.Errorf("quote currency for fund %s not found", code)
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			if token.Data != "option" {
				continue
			}

			var fundCode, currency string

			for _, attr := range token.Attr {
				switch attr.Key {
				case "data-code":
					fundCode = attr.Val
				case "data-currency":
					currency = attr.Val
				default:
				}
			}

			if fundCode == code && currency != "" {
				return currency, nil
			}
		case html.TextToken, html.EndTagToken, html.CommentToken, html.DoctypeToken:
		default:
		}
	}
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
	key := strings.ToLower(strings.TrimSpace(instrumentID))
	multiplier := 1.0
	source := i.name

	switch key {
	case "resat":
		key = "ata"
		source += " (Ata proxy)"
	case "gremse":
		key = "hasaltin"
		multiplier = 16
		source += " (16g has altın estimate)"
	default:
	}

	body, err := providerRequest(
		ctx,
		i.client,
		"https://www.izko.org.tr/api/web/v1/gold-prices",
		nil,
	)
	if err != nil {
		return models.Price{}, err
	}

	var result struct {
		Success bool `json:"success"`
		Data    []struct {
			Key  string  `json:"key"`
			Sell float64 `json:"sell_price"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.Price{}, err
	}

	if !result.Success {
		return models.Price{}, errors.New("gold provider returned an unsuccessful response")
	}

	for _, item := range result.Data {
		if strings.EqualFold(item.Key, key) {
			return checkedQuote(item.Sell*multiplier, "TRY", source)
		}
	}

	return models.Price{}, fmt.Errorf("gold instrument %s not found", instrumentID)
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
	symbol := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(instrumentID)), "BIST:")
	if symbol == "ALTIN.S1" {
		symbol = "ALTIN"
	}

	return tradingViewQuote(ctx, &t.baseGetter, "turkey", "BIST:"+symbol, "TRY")
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
	symbol := strings.ToUpper(strings.TrimSpace(instrumentID))
	symbol = strings.TrimPrefix(strings.TrimPrefix(symbol, "CURRENCY:"), "CRYPTO:")
	symbol = strings.TrimSuffix(strings.TrimSuffix(symbol, "-USD"), "USD")

	return tradingViewQuote(ctx, &t.baseGetter, "coin", "CRYPTO:"+symbol+"USD", "USD")
}

func tradingViewQuote(
	ctx context.Context,
	getter *baseGetter,
	market, ticker, currency string,
) (models.Price, error) {
	payload := map[string]any{
		"symbols": map[string]any{
			"tickers": []string{ticker},
			"query":   map[string]any{"types": []string{}},
		},
		"columns": []string{"close", "currency"},
	}

	body, err := providerRequest(
		ctx,
		getter.client,
		"https://scanner.tradingview.com/"+market+"/scan",
		payload,
	)
	if err != nil {
		return models.Price{}, err
	}

	var result struct {
		Data []struct {
			Symbol string            `json:"s"`
			Values []json.RawMessage `json:"d"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.Price{}, err
	}

	for _, row := range result.Data {
		if row.Symbol != ticker || len(row.Values) != 2 {
			continue
		}

		var (
			value         float64
			quoteCurrency string
		)

		if err := json.Unmarshal(row.Values[0], &value); err != nil {
			return models.Price{}, err
		}

		if err := json.Unmarshal(row.Values[1], &quoteCurrency); err != nil {
			return models.Price{}, err
		}

		if quoteCurrency != currency {
			return models.Price{}, fmt.Errorf(
				"unexpected quote currency %q for %s",
				quoteCurrency,
				ticker,
			)
		}

		return checkedQuote(value, currency, getter.name)
	}

	return models.Price{}, fmt.Errorf("instrument %s not found", ticker)
}

func checkedQuote(value float64, currency, source string) (models.Price, error) {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return models.Price{}, errors.New("provider returned an invalid price")
	}

	return models.Price{
		Value:     value,
		Currency:  currency,
		Source:    source,
		FetchedAt: time.Now().UTC(),
	}, nil
}

func providerRequest(
	ctx context.Context,
	client *http.Client,
	target string,
	payload any,
) ([]byte, error) {
	method := http.MethodGet

	var body io.Reader = http.NoBody

	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}

		body = bytes.NewReader(data)
		method = http.MethodPost
	}

	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, redactURLError(err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "application/json")

	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, redactURLError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("price provider returned HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read provider response: %w", err)
	}

	return data, nil
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

	headers := map[string]string{}
	if key := os.Getenv("COINGECKO_KEY"); key != "" {
		headers["X-Cg-Demo-Api-Key"] = key
	}

	resp, err := doGet(ctx, c.client, url, headers)
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return models.Price{}, fmt.Errorf("coingecko returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return models.Price{}, fmt.Errorf("read provider response: %w", err)
	}

	var result map[string]struct {
		USD float64 `json:"usd"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.Price{}, err
	}

	if data, ok := result[instrumentID]; ok {
		if data.USD <= 0 {
			return models.Price{}, fmt.Errorf("invalid USD price for %s", instrumentID)
		}

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

	resp, err := doGet(ctx, f.client, url, nil)
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return models.Price{}, fmt.Errorf("frankfurter returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return models.Price{}, fmt.Errorf("read provider response: %w", err)
	}

	var result struct {
		Rates map[string]float64 `json:"rates"`
		Date  string             `json:"date"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.Price{}, err
	}

	if rate, ok := result.Rates["TRY"]; ok {
		if rate <= 0 {
			return models.Price{}, fmt.Errorf("invalid TRY rate for %s", instrumentID)
		}

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
	price, err := strconv.ParseFloat(instrumentID, 64)
	if err != nil {
		return models.Price{}, fmt.Errorf("parse manual price: %w", err)
	}

	if price < 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return models.Price{}, fmt.Errorf("invalid manual price: %s", instrumentID)
	}

	return models.Price{
		Value:     price,
		Currency:  "TRY",
		Source:    m.name,
		FetchedAt: time.Now().UTC(),
	}, nil
}
