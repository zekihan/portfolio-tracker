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
	"sync"
	"time"

	"golang.org/x/net/html"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

const manualProviderName = "Manual"

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

func (registry *Registry) Register(g Getter) {
	registry.getters[g.Name()] = g
}

func (registry *Registry) Get(name string) (Getter, bool) {
	g, ok := registry.getters[name]

	return g, ok
}

func (registry *Registry) All() []Getter {
	result := make([]Getter, 0, len(registry.getters))
	for _, g := range registry.getters {
		result = append(result, g)
	}

	slices.SortFunc(result, func(a, b Getter) int {
		return strings.Compare(a.Name(), b.Name())
	})

	return result
}

func (registry *Registry) GetByType(providerType string) []Getter {
	var result []Getter

	for _, g := range registry.All() {
		if g.Type() == providerType {
			result = append(result, g)
		}
	}

	return result
}

func DefaultRegistry() *Registry {
	registry := NewRegistry()

	// Register all providers.
	registry.Register(NewYahooFinance())
	registry.Register(NewAlphaVantage())
	registry.Register(NewTwelveData())
	registry.Register(NewFinnhub())
	registry.Register(NewPusulaPortfoy())
	registry.Register(NewGarantiBBVA())
	registry.Register(NewIZKOGold())
	registry.Register(NewTradingViewBIST())
	registry.Register(NewTradingViewCrypto())
	registry.Register(NewCoinGecko())
	registry.Register(NewFrankfurter())
	registry.Register(NewManual())

	return registry
}

type baseGetter struct {
	client       *http.Client
	name         string
	providerType string
	ttl          time.Duration
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
			providerType: stockETFType,
			ttl:          providerTTL,
			client:       newProviderClient(),
		},
	}
}

func (getter *YahooFinance) GetPrice(
	ctx context.Context,
	instrumentID string,
) (models.Price, error) {
	instrumentID = strings.TrimSpace(instrumentID)
	for _, prefix := range []string{"NASDAQ:", "NYSE:", "NYSEARCA:", "AMEX:"} {
		if strings.HasPrefix(strings.ToUpper(instrumentID), prefix) {
			instrumentID = instrumentID[len(prefix):]

			break
		}
	}

	requestURL := fmt.Sprintf(
		"https://query1.finance.yahoo.com/v8/finance/chart/%s?interval=1m&range=1d",
		url.PathEscape(instrumentID),
	)

	headers := map[string]string{"User-Agent": "Mozilla/5.0"}

	resp, err := doGet(ctx, getter.client, requestURL, headers)
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return models.Price{}, providerHTTPError(errYahooFinanceReturnedHTTP, resp.StatusCode)
	}

	body, err := readProviderBody(resp.Body)
	if err != nil {
		return models.Price{}, fmt.Errorf("read yahoo finance response: %w", err)
	}

	var result struct {
		Chart struct {
			Error *struct {
				Code string `json:"code"`
			} `json:"error"`
			Result []struct {
				Meta struct {
					Currency           string  `json:"currency"`
					RegularMarketPrice float64 `json:"regularMarketPrice"`
				} `json:"meta"`
			} `json:"result"`
		} `json:"chart"`
	}

	decodeErr := json.Unmarshal(body, &result)
	if decodeErr != nil {
		return models.Price{}, wrapOperationError("GetPrice", decodeErr)
	}

	if result.Chart.Error != nil {
		apiErr := fmt.Errorf("%w %s", errYahooFinanceAPIErrorFor, instrumentID)
		if result.Chart.Error.Code == "Not Found" {
			return models.Price{}, instrumentFailure(apiErr)
		}

		return models.Price{}, apiErr
	}

	if result.Chart.Result == nil {
		return models.Price{}, errMalformedProviderResponse
	}

	if len(result.Chart.Result) == 0 {
		return models.Price{}, fmt.Errorf("%w %s", errNoDataFor, instrumentID)
	}

	quote := result.Chart.Result[0].Meta.RegularMarketPrice
	if quote <= 0 {
		return models.Price{}, fmt.Errorf("%w %s", errInvalidMarketPriceFor, instrumentID)
	}

	currency := result.Chart.Result[0].Meta.Currency
	if currency == "" {
		currency = currencyUSD
	}

	return checkedInstrumentQuote(quote, currency, getter.name, instrumentID)
}

//nolint:embeddedstructfieldcheck // Keep the bounded metadata map with its lock.
type AlphaVantage struct {
	currencies map[string]*sharedMetadata[string]
	baseGetter
	currencyMu sync.Mutex
}

func NewAlphaVantage() *AlphaVantage {
	return &AlphaVantage{
		name:         "Alpha Vantage",
		providerType: stockETFType,
		ttl:          providerTTL,
		client:       newProviderClient(),
	}
}

func (getter *AlphaVantage) GetPrice(
	ctx context.Context,
	instrumentID string,
) (models.Price, error) {
	key := os.Getenv("ALPHA_VANTAGE_KEY")
	if key == "" {
		return models.Price{}, fmt.Errorf("ALPHA_VANTAGE_KEY not set: %w", errAlphaVantageKeyNotSet)
	}

	requestURL := fmt.Sprintf(
		"https://www.alphavantage.co/query?function=GLOBAL_QUOTE&symbol=%s&apikey=%s",
		url.QueryEscape(instrumentID),
		url.QueryEscape(key),
	)

	resp, err := doGet(ctx, getter.client, requestURL, nil)
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return models.Price{}, providerHTTPError(errAlphaVantageReturnedHTTP, resp.StatusCode)
	}

	body, err := readProviderBody(resp.Body)
	if err != nil {
		return models.Price{}, fmt.Errorf("read provider response: %w", err)
	}

	var result struct {
		GlobalQuote *struct {
			Price    string `json:"05. price"`
			Currency string `json:"08. currency"`
		} `json:"Global Quote"`
		Note         string `json:"Note"`
		ErrorMessage string `json:"Error Message"`
		Information  string `json:"Information"`
	}

	decodeErr := json.Unmarshal(body, &result)
	if decodeErr != nil {
		return models.Price{}, wrapOperationError("GetPrice", decodeErr)
	}

	if result.ErrorMessage != "" || result.Information != "" {
		return models.Price{}, fmt.Errorf("%w %s", errAlphaVantageAPIErrorFor, instrumentID)
	}

	if result.Note != "" {
		return models.Price{}, fmt.Errorf("%w: %s", errRateLimited, result.Note)
	}

	if result.GlobalQuote == nil {
		return models.Price{}, errMalformedProviderResponse
	}

	quote, err := parseProviderQuote(result.GlobalQuote.Price,
		fmt.Errorf("%w %s", errInvalidAlphaVantagePriceFor, instrumentID))
	if err != nil {
		return models.Price{}, err
	}

	currency := strings.TrimSpace(result.GlobalQuote.Currency)
	if currency == "" {
		currency, err = getter.quoteCurrency(ctx, instrumentID, key)
		if err != nil {
			return models.Price{}, err
		}
	}

	return checkedQuote(quote, currency, getter.name)
}

type TwelveData struct{ baseGetter }

func NewTwelveData() *TwelveData {
	return &TwelveData{
		baseGetter{
			name:         "Twelve Data",
			providerType: stockETFType,
			ttl:          providerTTL,
			client:       newProviderClient(),
		},
	}
}

func (getter *TwelveData) GetPrice(ctx context.Context, instrumentID string) (models.Price, error) {
	key := os.Getenv("TWELVE_DATA_KEY")
	if key == "" {
		return models.Price{}, fmt.Errorf("TWELVE_DATA_KEY not set: %w", errTwelveDataKeyNotSet)
	}

	requestURL := fmt.Sprintf(
		"https://api.twelvedata.com/quote?symbol=%s&apikey=%s",
		url.QueryEscape(instrumentID),
		url.QueryEscape(key),
	)

	resp, err := doGet(ctx, getter.client, requestURL, nil)
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return models.Price{}, providerHTTPError(errTwelveDataReturnedHTTP, resp.StatusCode)
	}

	body, err := readProviderBody(resp.Body)
	if err != nil {
		return models.Price{}, fmt.Errorf("read provider response: %w", err)
	}

	var result struct {
		Price    *string `json:"close"`
		Currency string  `json:"currency"`
		Message  string  `json:"message"`
		Code     int     `json:"code"`
	}

	decodeErr := json.Unmarshal(body, &result)
	if decodeErr != nil {
		return models.Price{}, wrapOperationError("GetPrice", decodeErr)
	}

	if result.Code != 0 {
		return models.Price{}, providerHTTPError(
			fmt.Errorf("%w: %s", errAPIError, result.Message),
			result.Code,
		)
	}

	if result.Price == nil {
		return models.Price{}, errMalformedProviderResponse
	}

	quote, err := strconv.ParseFloat(*result.Price, 64)
	if err != nil {
		return models.Price{}, fmt.Errorf("parse twelve data price: %w", err)
	}

	if quote <= 0 || math.IsNaN(quote) || math.IsInf(quote, 0) {
		return models.Price{}, fmt.Errorf("%w %s", errInvalidTwelveDataPriceFor, instrumentID)
	}

	currency := strings.TrimSpace(result.Currency)
	if currency == "" {
		return models.Price{}, instrumentFailure(errUnknownQuoteCurrency)
	}

	return checkedQuote(quote, currency, getter.name)
}

type Finnhub struct{ baseGetter }

func NewFinnhub() *Finnhub {
	return &Finnhub{
		baseGetter{
			name:         "Finnhub",
			providerType: stockETFType,
			ttl:          providerTTL,
			client:       newProviderClient(),
		},
	}
}

func (getter *Finnhub) GetPrice(ctx context.Context, instrumentID string) (models.Price, error) {
	key := os.Getenv("FINNHUB_KEY")
	if key == "" {
		return models.Price{}, fmt.Errorf("FINNHUB_KEY not set: %w", errFinnhubKeyNotSet)
	}

	requestURL := "https://finnhub.io/api/v1/quote?symbol=" + url.QueryEscape(instrumentID)

	resp, err := doGet(ctx, getter.client, requestURL, map[string]string{"X-Finnhub-Token": key})
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return models.Price{}, providerHTTPError(errFinnhubReturnedHTTP, resp.StatusCode)
	}

	body, err := readProviderBody(resp.Body)
	if err != nil {
		return models.Price{}, fmt.Errorf("read provider response: %w", err)
	}

	var result struct {
		C     *float64 `json:"c"`
		Error string   `json:"error"`
	}

	decodeErr := json.Unmarshal(body, &result)
	if decodeErr != nil {
		return models.Price{}, wrapOperationError("GetPrice", decodeErr)
	}

	if result.Error != "" {
		return models.Price{}, errAPIError
	}

	if result.C == nil {
		return models.Price{}, errMalformedProviderResponse
	}

	if *result.C <= 0 {
		return models.Price{}, fmt.Errorf("%w %s", errNoPriceDataFor, instrumentID)
	}

	return checkedQuote(*result.C, currencyUSD, getter.name)
}

//nolint:embeddedstructfieldcheck // Keep pointer-bearing cache fields first for compact layout.
type PusulaPortfoy struct {
	funds sharedMetadata[pusulaFunds]

	baseGetter
}

type pusulaFunds struct {
	fetchedAt time.Time
	items     []pusulaFund
}

type pusulaFund struct {
	Code  string `json:"abbreviation"`
	Price string `json:"fundPrice"`
}

func NewPusulaPortfoy() *PusulaPortfoy {
	base := baseGetter{
		name:         "Pusula Portföy",
		providerType: "TR Fund",
		ttl:          providerTTL,
		client:       newProviderClient(),
	}

	return &PusulaPortfoy{baseGetter: base}
}

func (getter *PusulaPortfoy) GetPrice(
	ctx context.Context,
	instrumentID string,
) (models.Price, error) {
	funds, err := getter.funds.get(ctx, getter.ttl, getter.fetchFunds)
	if err != nil {
		return models.Price{}, err
	}

	for _, fund := range funds.items {
		if !strings.EqualFold(fund.Code, strings.TrimSpace(instrumentID)) {
			continue
		}

		value, err := strconv.ParseFloat(fund.Price, 64)
		if err != nil {
			return models.Price{}, fmt.Errorf("invalid fund price: %w", err)
		}

		quote, err := checkedQuote(value, "TRY", getter.name)
		quote.FetchedAt = funds.fetchedAt

		return quote, err
	}

	return models.Price{}, fmt.Errorf("%w %s not found", errFund, instrumentID)
}

func (getter *PusulaPortfoy) fetchFunds(ctx context.Context) (pusulaFunds, error) {
	body, err := providerRequest(ctx, getter.client,
		"https://www.pusulaportfoy.com.tr/api/funds?pageSize=200&active=true", nil)
	if err != nil {
		return pusulaFunds{}, err
	}

	var result struct {
		Items []pusulaFund `json:"items"`
	}

	err = json.Unmarshal(body, &result)
	if err != nil {
		return pusulaFunds{}, wrapOperationError("GetPrice", err)
	}

	if result.Items == nil {
		return pusulaFunds{}, errMalformedProviderResponse
	}

	return pusulaFunds{items: result.Items, fetchedAt: time.Now().UTC()}, nil
}

//nolint:embeddedstructfieldcheck // Keep pointer-bearing cache fields first for compact layout.
type GarantiBBVA struct {
	date       sharedMetadata[string]
	currencies sharedMetadata[[]byte]

	baseGetter
}

func NewGarantiBBVA() *GarantiBBVA {
	base := baseGetter{
		name:         "Garanti BBVA",
		providerType: "TR Fund",
		ttl:          providerTTL,
		client:       newProviderClient(),
	}

	return &GarantiBBVA{baseGetter: base}
}

func (getter *GarantiBBVA) GetPrice(
	ctx context.Context,
	instrumentID string,
) (models.Price, error) {
	code := strings.ToUpper(strings.TrimSpace(instrumentID))

	const base = "https://www.garantibbvaportfoy.com.tr/"

	date, err := getter.date.get(ctx, getter.ttl, getter.fetchDate)
	if err != nil {
		return models.Price{}, err
	}

	body, err := providerRequest(
		ctx,
		getter.client,
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
		Data *struct {
			Close float64 `json:"Close"`
		} `json:"data"`
	}

	decodeErr := json.Unmarshal(body, &result)
	if decodeErr != nil {
		return models.Price{}, wrapOperationError("GetPrice", decodeErr)
	}

	if result.Data == nil {
		return models.Price{}, errMalformedProviderResponse
	}

	body, err = getter.currencies.get(ctx, getter.ttl, getter.fetchCurrencies)
	if err != nil {
		return models.Price{}, err
	}

	// The quote endpoint omits currency; use the provider's fund metadata.
	currency, err := garantiCurrency(body, code)
	if err != nil {
		return models.Price{}, err
	}

	return checkedQuote(result.Data.Close, currency, getter.name)
}

func (getter *GarantiBBVA) fetchDate(ctx context.Context) (string, error) {
	body, err := providerRequest(ctx, getter.client,
		"https://www.garantibbvaportfoy.com.tr/webservice/lastdate", map[string]string{})
	if err != nil {
		return "", err
	}

	var date string

	err = json.Unmarshal(body, &date)
	if err != nil {
		return "", wrapOperationError("GetPrice", err)
	}

	_, err = time.Parse(time.DateOnly, date)
	if err != nil {
		return "", fmt.Errorf("invalid fund date: %w", err)
	}

	return date, nil
}

func (getter *GarantiBBVA) fetchCurrencies(ctx context.Context) ([]byte, error) {
	body, err := providerRequest(ctx, getter.client,
		"https://www.garantibbvaportfoy.com.tr/fon-getirileri", nil)
	if err != nil {
		return nil, err
	}
	// Validate that the page contains fund metadata before caching it. Currency
	// lookup remains per code; a quote or currency is never reused for another fund.
	_, err = findGarantiCurrency(body, "", true)
	if err != nil {
		return nil, err
	}

	return body, nil
}

func garantiCurrency(body []byte, code string) (string, error) {
	return findGarantiCurrency(body, code, false)
}

func findGarantiCurrency(body []byte, code string, anyFund bool) (string, error) {
	tokenizer := html.NewTokenizer(bytes.NewReader(body))
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return "", fmt.Errorf("%w %s not found", errQuoteCurrencyForFund, code)
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

			if (fundCode == code || anyFund) && fundCode != "" && currency != "" {
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
			ttl:          providerTTL,
			client:       newProviderClient(),
		},
	}
}

func (getter *IZKOGold) GetPrice(ctx context.Context, instrumentID string) (models.Price, error) {
	key := strings.ToLower(strings.TrimSpace(instrumentID))
	multiplier := 1.0
	source := getter.name

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
		getter.client,
		"https://www.izko.org.tr/api/web/v1/gold-prices",
		nil,
	)
	if err != nil {
		return models.Price{}, err
	}

	var result struct {
		Data []struct {
			Key  string  `json:"key"`
			Sell float64 `json:"sell_price"`
		} `json:"data"`
		Success bool `json:"success"`
	}

	decodeErr := json.Unmarshal(body, &result)
	if decodeErr != nil {
		return models.Price{}, wrapOperationError("GetPrice", decodeErr)
	}

	if !result.Success {
		return models.Price{}, errGoldProviderReturnedAnUnsuccessfulResponse
	}

	if result.Data == nil {
		return models.Price{}, errMalformedProviderResponse
	}

	for _, item := range result.Data {
		if strings.EqualFold(item.Key, key) {
			return checkedQuote(item.Sell*multiplier, "TRY", source)
		}
	}

	return models.Price{}, fmt.Errorf("%w %s not found", errGoldInstrument, instrumentID)
}

type TradingViewBIST struct{ baseGetter }

func NewTradingViewBIST() *TradingViewBIST {
	return &TradingViewBIST{
		baseGetter{
			name:         "TradingView BIST",
			providerType: "Gold",
			ttl:          providerTTL,
			client:       newProviderClient(),
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
			ttl:          providerTTL,
			client:       newProviderClient(),
		},
	}
}

func (getter *TradingViewCrypto) GetPrice(
	ctx context.Context,
	instrumentID string,
) (models.Price, error) {
	symbol := strings.ToUpper(strings.TrimSpace(instrumentID))
	symbol = strings.TrimPrefix(strings.TrimPrefix(symbol, "CURRENCY:"), "CRYPTO:")
	// Strip exactly one quote suffix: TUSD-USD is TrueUSD, not Threshold (T).
	// Keep the existing concatenated-pair interpretation for ambiguous raw TUSD.
	if base, explicitPair := strings.CutSuffix(symbol, "-USD"); explicitPair {
		symbol = base
	} else {
		symbol = strings.TrimSuffix(symbol, currencyUSD)
	}

	if symbol == "" {
		return models.Price{}, instrumentFailure(
			fmt.Errorf("%w: missing crypto symbol", errInstrument),
		)
	}

	return tradingViewQuote(
		ctx,
		&getter.baseGetter,
		"coin",
		"CRYPTO:"+symbol+currencyUSD,
		currencyUSD,
	)
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

	decodeErr := json.Unmarshal(body, &result)
	if decodeErr != nil {
		return models.Price{}, wrapOperationError("tradingViewQuote", decodeErr)
	}

	if result.Data == nil {
		return models.Price{}, errMalformedProviderResponse
	}

	for _, row := range result.Data {
		if row.Symbol != ticker {
			continue
		}

		if len(row.Values) != 2 {
			return models.Price{}, errMalformedProviderResponse
		}

		var (
			value         float64
			quoteCurrency string
		)

		err := json.Unmarshal(row.Values[0], &value)
		if err != nil {
			return models.Price{}, wrapOperationError("tradingViewQuote", err)
		}

		err = json.Unmarshal(row.Values[1], &quoteCurrency)
		if err != nil {
			return models.Price{}, wrapOperationError("tradingViewQuote", err)
		}

		if quoteCurrency != currency {
			return models.Price{}, fmt.Errorf(
				"%w %q for %s",
				errUnexpectedQuoteCurrency,
				quoteCurrency,
				ticker,
			)
		}

		return checkedQuote(value, currency, getter.name)
	}

	return models.Price{}, fmt.Errorf("%w %s not found", errInstrument, ticker)
}

func checkedQuote(value float64, currency, source string) (models.Price, error) {
	if value > maxProviderQuote {
		return models.Price{}, errUnsafeProviderQuote
	}

	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return models.Price{}, errProviderReturnedAnInvalidPrice
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
			return nil, wrapOperationError("providerRequest", err)
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
		return nil, fmt.Errorf("%w %d", errPriceProviderReturnedHTTP, resp.StatusCode)
	}

	data, err := readProviderBody(resp.Body)
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
			ttl:          providerTTL,
			client:       newProviderClient(),
		},
	}
}

func (getter *CoinGecko) GetPrice(ctx context.Context, instrumentID string) (models.Price, error) {
	requestURL := fmt.Sprintf(
		"https://api.coingecko.com/api/v3/simple/price?ids=%s&vs_currencies=usd",
		url.QueryEscape(instrumentID),
	)

	headers := map[string]string{}
	if key := os.Getenv("COINGECKO_KEY"); key != "" {
		headers["X-Cg-Demo-Api-Key"] = key
	}

	resp, err := doGet(ctx, getter.client, requestURL, headers)
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return models.Price{}, providerHTTPError(errCoingeckoReturnedHTTP, resp.StatusCode)
	}

	body, err := readProviderBody(resp.Body)
	if err != nil {
		return models.Price{}, fmt.Errorf("read provider response: %w", err)
	}

	var envelope struct {
		Status struct {
			ErrorCode int `json:"error_code"`
		} `json:"status"`
	}

	envelopeErr := json.Unmarshal(body, &envelope)
	if envelopeErr != nil {
		return models.Price{}, wrapOperationError("GetPrice", envelopeErr)
	}

	if envelope.Status.ErrorCode != 0 {
		return models.Price{}, errAPIError
	}

	var result map[string]struct {
		USD float64 `json:"usd"`
	}

	decodeErr := json.Unmarshal(body, &result)
	if decodeErr != nil {
		return models.Price{}, wrapOperationError("GetPrice", decodeErr)
	}

	if data, ok := result[instrumentID]; ok {
		if data.USD <= 0 {
			return models.Price{}, fmt.Errorf("%w %s", errInvalidUSDPriceFor, instrumentID)
		}

		return checkedQuote(data.USD, currencyUSD, getter.name)
	}

	return models.Price{}, fmt.Errorf("%w %s not found", errInstrument, instrumentID)
}

type Frankfurter struct{ baseGetter }

func NewFrankfurter() *Frankfurter {
	return &Frankfurter{
		baseGetter{
			name:         "Frankfurter",
			providerType: "FX",
			ttl:          providerTTL,
			client:       newProviderClient(),
		},
	}
}

func (getter *Frankfurter) GetPrice(
	ctx context.Context,
	instrumentID string,
) (models.Price, error) {
	requestURL := fmt.Sprintf(
		"https://api.frankfurter.dev/v1/latest?from=%s&to=TRY",
		url.QueryEscape(instrumentID),
	)

	resp, err := doGet(ctx, getter.client, requestURL, nil)
	if err != nil {
		return models.Price{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return models.Price{}, providerHTTPError(errFrankfurterReturnedHTTP, resp.StatusCode)
	}

	body, err := readProviderBody(resp.Body)
	if err != nil {
		return models.Price{}, fmt.Errorf("read provider response: %w", err)
	}

	var result struct {
		Rates map[string]float64 `json:"rates"`
		Date  string             `json:"date"`
	}

	decodeErr := json.Unmarshal(body, &result)
	if decodeErr != nil {
		return models.Price{}, wrapOperationError("GetPrice", decodeErr)
	}

	if result.Rates == nil {
		return models.Price{}, errMalformedProviderResponse
	}

	if rate, ok := result.Rates["TRY"]; ok {
		if rate <= 0 {
			return models.Price{}, fmt.Errorf("%w %s", errInvalidTRYRateFor, instrumentID)
		}

		if rate < minFXRate {
			return models.Price{}, errUnsafeProviderQuote
		}

		return checkedQuote(rate, "TRY", getter.name)
	}

	return models.Price{}, fmt.Errorf("%w %s", errNoRateFor, instrumentID)
}

type Manual struct{ baseGetter }

func NewManual() *Manual {
	return &Manual{
		baseGetter{
			name:         manualProviderName,
			providerType: manualProviderName,
			ttl:          0,
			client:       nil,
		},
	}
}

func (getter *Manual) GetPrice(ctx context.Context, instrumentID string) (models.Price, error) {
	quote, err := strconv.ParseFloat(instrumentID, 64)
	if err != nil {
		return models.Price{}, instrumentFailure(fmt.Errorf("parse manual price: %w", err))
	}

	if quote > maxProviderQuote {
		return models.Price{}, errUnsafeProviderQuote
	}

	if quote < 0 || math.IsNaN(quote) || math.IsInf(quote, 0) {
		return models.Price{}, fmt.Errorf("%w: %s", errInvalidManualPrice, instrumentID)
	}

	return models.Price{
		Value:     quote,
		Currency:  "TRY",
		Source:    getter.name,
		FetchedAt: time.Now().UTC(),
	}, nil
}

var (
	errAlphaVantageKeyNotSet                      = errors.New("alpha vantage key not set")
	errFinnhubReturnedHTTP                        = errors.New("finnhub returned HTTP")
	errProviderReturnedAnInvalidPrice             = errors.New("provider returned an invalid price")
	errInvalidManualPrice                         = errors.New("invalid manual price")
	errAPIError                                   = errors.New("api error")
	errNoPriceDataFor                             = errors.New("no price data for")
	errFinnhubKeyNotSet                           = errors.New("finnhub key not set")
	errUnexpectedQuoteCurrency                    = errors.New("unexpected quote currency")
	errNoRateFor                                  = errors.New("no rate for")
	errNoDataFor                                  = errors.New("no data for")
	errInvalidMarketPriceFor                      = errors.New("invalid market price for")
	errRateLimited                                = errors.New("rate limited")
	errGoldProviderReturnedAnUnsuccessfulResponse = errors.New(
		"gold provider returned an unsuccessful response",
	)
	errTwelveDataKeyNotSet         = errors.New("twelve data key not set")
	errInstrument                  = errors.New("instrument")
	errPriceProviderReturnedHTTP   = errors.New("price provider returned HTTP")
	errCoingeckoReturnedHTTP       = errors.New("coingecko returned HTTP")
	errInvalidTRYRateFor           = errors.New("invalid TRY rate for")
	errYahooFinanceAPIErrorFor     = errors.New("yahoo finance API error for")
	errAlphaVantageReturnedHTTP    = errors.New("alpha vantage returned HTTP")
	errAlphaVantageAPIErrorFor     = errors.New("alpha vantage API error for")
	errGoldInstrument              = errors.New("gold instrument")
	errYahooFinanceReturnedHTTP    = errors.New("yahoo finance returned HTTP")
	errTwelveDataReturnedHTTP      = errors.New("twelve data returned HTTP")
	errInvalidTwelveDataPriceFor   = errors.New("invalid twelve data price for")
	errFund                        = errors.New("fund")
	errInvalidAlphaVantagePriceFor = errors.New("invalid alpha vantage price for")
	errQuoteCurrencyForFund        = errors.New("quote currency for fund")
	errInvalidUSDPriceFor          = errors.New("invalid USD price for")
	errFrankfurterReturnedHTTP     = errors.New("frankfurter returned HTTP")
)

const (
	providerTTL     = 15 * time.Minute
	providerTimeout = 10 * time.Second
	stockETFType    = "Stock/ETF"
	currencyUSD     = "USD"
)

func parseProviderQuote(raw string, invalidPrice error) (float64, error) {
	if raw == "" {
		return 0, instrumentFailure(errNoPriceDataFor)
	}

	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("parse provider quote: %w", err)
	}

	if value > maxProviderQuote {
		return 0, errUnsafeProviderQuote
	}

	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, instrumentFailure(invalidPrice)
	}

	return value, nil
}
