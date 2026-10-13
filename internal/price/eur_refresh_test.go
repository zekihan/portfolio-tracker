package price

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
)

type currencyRefreshGetter struct {
	currency string
	name     string

	usdCalls atomic.Int32
	eurCalls atomic.Int32
	failEUR  bool
}

func (getter *currencyRefreshGetter) Name() string       { return getter.name }
func (getter *currencyRefreshGetter) Type() string       { return "FX" }
func (getter *currencyRefreshGetter) TTL() time.Duration { return providerTTL }

func (getter *currencyRefreshGetter) GetPrice(
	_ context.Context,
	instrument string,
) (models.Price, error) {
	switch instrument {
	case "USD", "USDTRY=X":
		getter.usdCalls.Add(1)
	case "EUR", "EURTRY=X":
		getter.eurCalls.Add(1)

		if getter.failEUR {
			return models.Price{}, errProviderUnavailable
		}
	}

	return models.Price{
		Value: 50, Currency: getter.currency, Source: getter.Name(), FetchedAt: time.Now(),
	}, nil
}

func TestRefreshAllRefreshesRequiredEURRate(t *testing.T) {
	t.Parallel()

	for _, scenario := range []string{"EUR base", "EUR quote", "EUR display", "TRY only"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()

			fx := &currencyRefreshGetter{name: "Frankfurter", currency: "TRY"}
			asset := &currencyRefreshGetter{name: "EUR asset", currency: "EUR"}
			service := newRefreshTestService(t, fx, asset)
			holding := models.Holding{BaseCurrency: "TRY", PriceProvider: "Manual"}

			switch scenario {
			case "EUR base":
				holding.BaseCurrency = "EUR"
			case "EUR quote":
				holding.PriceProvider, holding.InstrumentID = asset.Name(), "TEST"
			case "EUR display":
				err := db.SetSettingContext(t.Context(), service.db, "display_currency", "EUR")
				if err != nil {
					t.Fatal(err)
				}
			}

			seedRecoveryFX(t, service, fx.Name(), "USD", 40, 0)
			seedRecoveryFX(t, service, fx.Name(), "EUR", 45, 0)

			err := service.RefreshAll(t.Context(), []models.Holding{holding})
			if err != nil {
				t.Fatal(err)
			}

			wantEURCalls := int32(1)
			if scenario == "TRY only" {
				wantEURCalls = 0
			}

			if fx.usdCalls.Load() != 1 || fx.eurCalls.Load() != wantEURCalls {
				t.Fatalf("FX calls USD=%d, EUR=%d; want USD=1, EUR=%d",
					fx.usdCalls.Load(), fx.eurCalls.Load(), wantEURCalls)
			}

			quote, err := service.GetEURTRYQuote(t.Context())

			wantValue := 50.0
			if scenario == "TRY only" {
				wantValue = 45
			}

			checkFreshFXQuote(t, &quote, err, wantValue)
		})
	}
}

func TestRefreshAllRequiresFreshEURWhenNeeded(t *testing.T) {
	t.Parallel()

	for _, scenario := range []string{"fallback", "both fail", "TRY only"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()

			primary := &currencyRefreshGetter{
				name: "Frankfurter", currency: "TRY", failEUR: true,
			}
			fallback := &currencyRefreshGetter{
				name:     "Yahoo Finance",
				currency: "TRY",
				failEUR:  scenario != "fallback",
			}
			service := newRefreshTestService(t, primary, fallback)

			currency := "EUR"
			if scenario == "TRY only" {
				currency = "TRY"
			}

			seedRecoveryFX(t, service, primary.Name(), "EUR", 45, 0)
			seedRecoveryFX(t, service, fallback.Name(), "EURTRY=X", 46, 0)

			err := service.RefreshAll(t.Context(), []models.Holding{
				{BaseCurrency: currency, PriceProvider: "Manual"},
			})
			if (err != nil) != (scenario == "both fail") {
				t.Fatalf("EUR refresh error = %v, scenario %s", err, scenario)
			}

			if scenario == "fallback" && fallback.eurCalls.Load() != 1 {
				t.Fatal("EUR refresh did not fetch the fallback")
			}

			if scenario == "TRY only" && primary.eurCalls.Load() != 0 {
				t.Fatal("TRY refresh unnecessarily fetched EUR")
			}
		})
	}
}
