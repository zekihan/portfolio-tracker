package price

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

func TestEveryProviderRejectsLargestFiniteQuote(t *testing.T) {
	t.Setenv("ALPHA_VANTAGE_KEY", "test-alpha")
	t.Setenv("TWELVE_DATA_KEY", "test-twelve")
	t.Setenv("FINNHUB_KEY", "test-finnhub")

	extreme := strconv.FormatFloat(math.MaxFloat64, 'g', -1, 64)
	for _, test := range []struct {
		getter     Getter
		instrument string
		body       string
	}{
		{NewYahooFinance(), "TEST", `{"chart":{"result":[{"meta":{"regularMarketPrice":` +
			extreme + `,"currency":"USD"}}]}}`},
		{NewAlphaVantage(), "TEST", `{"Global Quote":{"05. price":"` + extreme + `","08. currency":"USD"}}`},
		{NewTwelveData(), "TEST", `{"close":"` + extreme + `","currency":"USD"}`},
		{NewFinnhub(), "TEST", `{"c":` + extreme + `}`},
		{NewPusulaPortfoy(), "TEST", `{"items":[{"abbreviation":"TEST","fundPrice":"` + extreme + `"}]}`},
		{NewGarantiBBVA(), "TEST", `{"data":{"Close":` + extreme + `}}`},
		{NewIZKOGold(), "gram", `{"success":true,"data":[{"key":"gram","sell_price":` + extreme + `}]}`},
		{NewTradingViewBIST(), "TEST", `{"data":[{"s":"BIST:TEST","d":[` + extreme + `,"TRY"]}]}`},
		{NewTradingViewCrypto(), "TEST-USD", `{"data":[{"s":"CRYPTO:TESTUSD","d":[` + extreme + `,"USD"]}]}`},
		{NewCoinGecko(), "TEST", `{"TEST":{"usd":` + extreme + `}}`},
		{NewFrankfurter(), "USD", `{"rates":{"TRY":` + extreme + `}}`},
		{NewManual(), extreme, ""},
	} {
		t.Run(test.getter.Name(), func(t *testing.T) {
			if test.getter.Name() != manualProviderName {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						switch r.URL.Path {
						case "/webservice/lastdate":
							fmt.Fprint(w, `"2026-10-25"`)
						case "/fon-getirileri":
							fmt.Fprint(w, `<option data-code="TEST" data-currency="TRY"></option>`)
						default:
							fmt.Fprint(w, test.body)
						}
					}),
				)
				defer server.Close()

				setRegressionTransport(t, providerTestClient(test.getter), server.URL)
			}

			_, err := test.getter.GetPrice(t.Context(), test.instrument)
			if !errors.Is(err, errUnsafeProviderQuote) {
				t.Fatalf("largest finite quote error = %v", err)
			}
		})
	}
}

type unsafeQuoteGetter struct {
	name  string
	quote models.Price
}

func (getter *unsafeQuoteGetter) Name() string { return getter.name }
func (*unsafeQuoteGetter) Type() string        { return stockETFType }
func (*unsafeQuoteGetter) TTL() time.Duration  { return providerTTL }
func (getter *unsafeQuoteGetter) GetPrice(context.Context, string) (models.Price, error) {
	return getter.quote, nil
}

func TestUnsafeQuotePreservesCacheAndHealth(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		provider   string
		instrument string
		value      float64
		quantity   float64
	}{
		{"extreme finite", "Yahoo Finance", "TEST", math.MaxFloat64, 1},
		{"quantity overflow", "Yahoo Finance", "TEST", 1000, math.MaxFloat64 / 100},
		{"FX underflow divisor", "Frankfurter", "USD", math.SmallestNonzeroFloat64, 1},
		{"Yahoo FX divisor", "Yahoo Finance", "USDTRY=X", math.SmallestNonzeroFloat64, 1},
		{"normalized instrument", "TradingView Crypto", "CRYPTO:TUSDUSD", 1000, math.MaxFloat64 / 100},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			getter := &unsafeQuoteGetter{
				name:  test.provider,
				quote: models.Price{Value: test.value, Currency: "USD"},
			}
			service := newRefreshTestService(t, getter)

			instrument := test.instrument
			if test.provider == "TradingView Crypto" {
				instrument = "TUSD-USD"
			}

			_, err := service.db.ExecContext(t.Context(),
				`INSERT INTO holdings (name,asset_type,quantity,price_provider,instrument_id)
     VALUES ('Test','Stock',?,?,?)`, test.quantity, test.provider, instrument)
			if err != nil {
				t.Fatal(err)
			}

			old := models.Price{
				Value:     1,
				Currency:  "USD",
				Source:    "good",
				FetchedAt: time.Now().Add(-time.Minute),
			}

			err = service.cachePrice(
				test.provider,
				test.instrument,
				&old,
				providerTTL,
			)
			if err != nil {
				t.Fatal(err)
			}

			before, err := service.getCachedPrice(test.provider, test.instrument)
			if err != nil {
				t.Fatal(err)
			}

			service.recordFailure(test.provider, test.instrument, errProviderUnavailable)

			got, err := service.getPrice(t.Context(), test.provider, instrument, true)
			if err != nil || got.Value != old.Value || !got.IsFailed || !got.IsStale ||
				!errors.Is(got.Error, errUnsafeProviderQuote) {
				t.Fatalf("unsafe refresh result = %+v, %v", got, err)
			}

			after, err := service.getCachedPrice(test.provider, test.instrument)
			if err != nil {
				t.Fatal(err)
			}

			assertRejectedQuoteCache(t, before, after)

			if service.failures[test.provider] != 2 || canUseCachedQuote(after, false) {
				t.Fatalf("unsafe candidate reset health or retained fresh status: %+v", after)
			}
		})
	}
}

func TestQuoteSafetyBoundaries(t *testing.T) {
	t.Parallel()

	for _, value := range []float64{math.NaN(), math.Inf(1), -1, 0, math.Nextafter(maxProviderQuote, math.Inf(1))} {
		_, err := checkedQuote(value, "TRY", "test")
		if err == nil {
			t.Errorf("accepted invalid quote %v", value)
		}
	}

	_, err := checkedQuote(maxProviderQuote, "TRY", "test")
	if err != nil {
		t.Fatalf("rejected boundary quote: %v", err)
	}

	if !safeHoldingQuote(100, 2, "USD") || safeHoldingQuote(1000, math.MaxFloat64/100, "USD") {
		t.Fatal("incorrect holding overflow guard")
	}
}

func FuzzAcceptedQuotesHaveFiniteNormalizedTotals(f *testing.F) {
	for _, currency := range []uint8{0, 1, 2} {
		f.Add(100.0, 2.0, 40.0, 45.0, currency)
		f.Add(maxProviderQuote, math.MaxFloat64, minFXRate, maxProviderQuote, currency)
		f.Add(math.SmallestNonzeroFloat64, math.MaxFloat64, minFXRate, minFXRate, currency)
		f.Add(1.0, math.MaxFloat64/1e24, maxProviderQuote, minFXRate, currency)
	}

	f.Fuzz(func(t *testing.T, value, quantity, usdRate, eurRate float64, choice uint8) {
		if !validFuzzQuoteAndRates(value, usdRate, eurRate) {
			return
		}

		currency := []string{"TRY", "USD", "EUR"}[choice%3]
		if !safeHoldingQuote(value, quantity, currency) {
			return
		}

		tryPrice, usdPrice := value, value/usdRate

		switch currency {
		case "USD":
			tryPrice, usdPrice = value*usdRate, value
		case "EUR":
			tryPrice, usdPrice = value*eurRate, (value*eurRate)/usdRate
		}

		for _, normalized := range []float64{tryPrice, usdPrice, tryPrice / eurRate} {
			total := quantity * normalized
			if math.IsInf(normalized, 0) || math.IsNaN(normalized) || math.IsInf(total, 0) ||
				math.IsNaN(total) {
				t.Fatalf(
					"accepted nonfinite normalized value: quote=%g quantity=%g rates=%g/%g currency=%s",
					value,
					quantity,
					usdRate,
					eurRate,
					currency,
				)
			}
		}
	})
}

func validFuzzQuoteAndRates(value, usdRate, eurRate float64) bool {
	return value > 0 && value <= maxProviderQuote &&
		usdRate >= minFXRate && usdRate <= maxProviderQuote &&
		eurRate >= minFXRate && eurRate <= maxProviderQuote
}

func TestFXCandidateCannotPoisonManualHoldingValuation(t *testing.T) {
	t.Parallel()

	for _, currency := range []string{"USD", "EUR"} {
		t.Run(currency, func(t *testing.T) {
			t.Parallel()

			getter := &unsafeQuoteGetter{
				name:  "Frankfurter",
				quote: models.Price{Value: maxProviderQuote, Currency: "TRY"},
			}
			service := newRefreshTestService(t, getter)

			_, err := service.db.ExecContext(
				t.Context(),
				`INSERT INTO holdings (name,asset_type,quantity,price_provider,base_currency,manual_price)
     VALUES ('Manual','Cash',1,'Manual',?,?)`,
				currency,
				math.MaxFloat64/100,
			)
			if err != nil {
				t.Fatal(err)
			}

			old := models.Price{Value: 40, Currency: "TRY", Source: getter.Name()}

			err = service.cachePrice(getter.Name(), currency, &old, providerTTL)
			if err != nil {
				t.Fatal(err)
			}

			quote, err := service.getPrice(t.Context(), getter.Name(), currency, true)
			if err != nil || quote.Value != 40 || !quote.IsStale ||
				!errors.Is(quote.Error, errUnsafeProviderQuote) {
				t.Fatalf("FX poisoned manual holding: %+v, %v", quote, err)
			}
		})
	}
}

func assertRejectedQuoteCache(t *testing.T, before, after *models.PriceCacheEntry) {
	t.Helper()

	if after.Price != before.Price || after.Currency != before.Currency ||
		after.Source != before.Source || !after.FetchedAt.Equal(before.FetchedAt) ||
		!after.ExpiresAt.Equal(before.ExpiresAt) {
		t.Fatalf("cache changed: before=%+v after=%+v", before, after)
	}

	if after.ErrorCount != 2 || after.LastError != errUnsafeProviderQuote.Error() {
		t.Fatalf("cache lost failure metadata: %+v", after)
	}
}
