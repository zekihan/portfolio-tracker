package price

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
)

func TestTwelveDataCurrencyBearingQuote(t *testing.T) {
	t.Setenv("TWELVE_DATA_KEY", "test")

	for _, currency := range []string{"TRY", "USD", "", " "} {
		t.Run(currency, func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/quote" {
						t.Errorf("path = %s, want /quote", r.URL.Path)
					}

					fmt.Fprintf(w, `{"symbol":"TEST","close":"125.5","currency":%q}`, currency)
				}),
			)
			defer server.Close()

			getter := NewTwelveData()
			setRegressionTransport(t, getter.client, server.URL)

			quote, err := getter.GetPrice(t.Context(), "TEST")
			if currency == "" || currency == " " {
				if err == nil || !isInstrumentFailure(err) {
					t.Fatalf("unknown currency must be instrument-local: %v", err)
				}
			} else if err != nil || quote.Currency != currency || quote.Value != 125.5 {
				t.Fatalf("quote = %+v, %v", quote, err)
			}
		})
	}
}

//nolint:gocognit,gocyclo // Exercise both provider caches through both reset paths.
func TestFundRefreshInvalidatesProviderMetadata(t *testing.T) {
	for _, provider := range []string{"Pusula", "Garanti"} {
		for _, reset := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reset=%v", provider, reset), func(t *testing.T) {
				var revision, metadataCalls atomic.Int32
				revision.Store(1)

				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						current := revision.Load()

						switch r.URL.Path {
						case "/api/funds":
							metadataCalls.Add(1)
							fmt.Fprintf(
								w,
								`{"items":[{"abbreviation":"AAA","fundPrice":"%d"},{"abbreviation":"BBB","fundPrice":"%d"}]}`,
								current*10,
								current*10,
							)
						case "/webservice/lastdate":
							metadataCalls.Add(1)
							fmt.Fprintf(w, `"2026-10-%02d"`, current)
						case "/fon-getirileri":
							fmt.Fprint(
								w,
								`<option data-code="AAA" data-currency="TRY"></option><option data-code="BBB" data-currency="TRY"></option>`,
							)
						case "/webservice/funddailyvalues":
							var payload struct {
								Date string `json:"date"`
							}

							err := json.NewDecoder(r.Body).Decode(&payload)
							if err != nil {
								t.Error(err)
							}

							value := 10
							if payload.Date == "2026-10-02" {
								value = 20
							}

							fmt.Fprintf(w, `{"data":{"Close":%d}}`, value)
						default:
							t.Errorf("unexpected path %s", r.URL.Path)
						}
					}),
				)
				defer server.Close()

				var getter Getter = NewPusulaPortfoy()
				if provider == "Garanti" {
					getter = NewGarantiBBVA()
				}

				setRegressionTransport(t, providerTestClient(getter), server.URL)
				service := newRegressionService(t, getter)

				_, err := service.GetPrice(t.Context(), getter.Name(), "AAA")
				if err != nil {
					t.Fatal(err)
				}

				revision.Store(2)

				holdings := []models.Holding{
					{PriceProvider: getter.Name(), InstrumentID: "AAA"},
					{PriceProvider: getter.Name(), InstrumentID: "BBB"},
				}

				if reset {
					err = service.WithCacheReset(
						t.Context(),
						func() error {
							_, resetErr := service.db.ExecContext(
								t.Context(),
								"DELETE FROM price_cache",
							)

							return wrapOperationError("reset test cache", resetErr)
						},
					)
				} else {
					err = service.RefreshAll(t.Context(), holdings)
				}

				if err != nil {
					t.Fatal(err)
				}

				for _, holding := range holdings {
					after, err := service.GetPrice(
						t.Context(),
						holding.PriceProvider,
						holding.InstrumentID,
					)
					if err != nil || after.Value != 20 {
						t.Fatalf("refreshed quote = %+v, %v", after, err)
					}
				}

				if calls := metadataCalls.Load(); calls != 2 {
					t.Fatalf("metadata fetches = %d, want 2", calls)
				}
			})
		}
	}
}

func TestPusulaPreservesActualFetchTimestamp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(
			w,
			`{"items":[{"abbreviation":"AAA","fundPrice":"12"},{"abbreviation":"BBB","fundPrice":"23"}]}`,
		)
	}))
	defer server.Close()

	getter := NewPusulaPortfoy()
	setRegressionTransport(t, getter.client, server.URL)

	first, err := getter.GetPrice(t.Context(), "AAA")
	if err != nil {
		t.Fatal(err)
	}

	second, err := getter.GetPrice(t.Context(), "BBB")
	if err != nil || !first.FetchedAt.Equal(second.FetchedAt) {
		t.Fatalf(
			"same response received different timestamps: %v / %v (%v)",
			first.FetchedAt,
			second.FetchedAt,
			err,
		)
	}
}

type regressionFXGetter struct {
	baseGetter

	calls atomic.Int32
	fail  bool
}

func (getter *regressionFXGetter) GetPrice(context.Context, string) (models.Price, error) {
	getter.calls.Add(1)

	if getter.fail {
		return models.Price{}, errProviderUnavailable
	}

	return models.Price{
		Value:     40,
		Currency:  "TRY",
		Source:    getter.Name(),
		FetchedAt: time.Now().UTC(),
	}, nil
}

func TestRefreshAllRefreshesManualHoldingFX(t *testing.T) {
	for _, scenario := range []string{"primary", "fallback", "both fail"} {
		t.Run(scenario, func(t *testing.T) {
			primary := &regressionFXGetter{
				name: "Frankfurter", ttl: time.Hour,
				fail: scenario != "primary",
			}
			fallback := &regressionFXGetter{
				name: "Yahoo Finance", ttl: time.Hour,
				fail: scenario == "both fail",
			}
			service := newRegressionService(t, primary, fallback)

			for _, key := range []refreshKey{{"Frankfurter", "USD"}, {"Yahoo Finance", "USDTRY=X"}} {
				quote := models.Price{
					Value:     30,
					Currency:  "TRY",
					Source:    key.provider,
					FetchedAt: time.Now().UTC(),
				}

				err := service.cachePrice(
					key.provider,
					key.instrument,
					&quote,
					time.Hour,
				)
				if err != nil {
					t.Fatal(err)
				}
			}

			err := service.RefreshAll(
				t.Context(),
				[]models.Holding{{PriceProvider: "Manual", BaseCurrency: "TRY"}},
			)
			if (err != nil) != (scenario == "both fail") {
				t.Fatalf("refresh error = %v", err)
			}

			if primary.calls.Load() != 1 {
				t.Fatalf("primary fetches = %d", primary.calls.Load())
			}

			if scenario != "primary" && fallback.calls.Load() != 1 {
				t.Fatalf("fallback fetches = %d", fallback.calls.Load())
			}

			if scenario != "both fail" {
				name, instrument := "Frankfurter", "USD"
				if scenario == "fallback" {
					name, instrument = "Yahoo Finance", "USDTRY=X"
				}

				cached, err := service.getCachedPrice(name, instrument)
				if err != nil || cached == nil || cached.Price != 40 {
					t.Fatalf("FX cache = %+v, %v", cached, err)
				}
			}
		})
	}
}

func newRegressionService(t *testing.T, getters ...Getter) *Service {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	registry := NewRegistry()
	for _, getter := range getters {
		registry.Register(getter)
	}

	service := NewService(database, registry)

	t.Cleanup(func() { _ = service.Close() })

	return service
}

func setRegressionTransport(t *testing.T, client *http.Client, rawURL string) {
	t.Helper()

	target, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}

	client.Transport = &localProviderTransport{target: target}
}

//nolint:gocognit,gocyclo // Table covers currency matching and cache reuse together.
func TestAlphaVantageResolvesExactCurrency(t *testing.T) {
	t.Setenv("ALPHA_VANTAGE_KEY", "test")

	for _, scenario := range []string{"exact", "missing", "ambiguous"} {
		t.Run(scenario, func(t *testing.T) {
			var searches atomic.Int32

			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Query().Get("function") == "GLOBAL_QUOTE" {
						fmt.Fprint(w, `{"Global Quote":{"05. price":"12.5"}}`)

						return
					}

					searches.Add(1)

					if r.URL.Query().Get("keywords") != "TEST" {
						t.Error("wrong metadata symbol")
					}

					switch scenario {
					case "exact":
						fmt.Fprint(
							w,
							`{"bestMatches":[{"1. symbol":"TEST.X","8. currency":"USD"},{"1. symbol":"TEST","8. currency":"TRY"}]}`,
						)
					case "missing":
						fmt.Fprint(
							w,
							`{"bestMatches":[{"1. symbol":"TEST.X","8. currency":"USD"}]}`,
						)
					case "ambiguous":
						fmt.Fprint(
							w,
							`{"bestMatches":[{"1. symbol":"TEST","8. currency":"USD"},{"1. symbol":"TEST","8. currency":"TRY"}]}`,
						)
					}
				}),
			)
			defer server.Close()

			getter := NewAlphaVantage()
			setRegressionTransport(t, getter.client, server.URL)

			for range 2 {
				quote, err := getter.GetPrice(t.Context(), "TEST")
				if scenario == "exact" {
					if err != nil || quote.Currency != "TRY" || quote.Value != 12.5 {
						t.Fatalf("quote=%+v, %v", quote, err)
					}
				} else if err == nil || !isInstrumentFailure(err) {
					t.Fatalf("unsafe currency accepted: %+v, %v", quote, err)
				}
			}

			if scenario == "exact" && searches.Load() != 1 {
				t.Fatalf("metadata searches=%d, want 1", searches.Load())
			}
		})
	}
}

func TestForcedRefreshDoesNotJoinOrdinaryMetadataFlight(t *testing.T) {
	var revision atomic.Int32
	revision.Store(1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"items":[{"abbreviation":"AAA","fundPrice":"%d"}]}`, revision.Load()*10)
	}))
	defer server.Close()

	getter := NewPusulaPortfoy()
	setRegressionTransport(t, getter.client, server.URL)

	_, err := getter.GetPrice(t.Context(), "AAA")
	if err != nil {
		t.Fatal(err)
	}

	service := newRegressionService(t, getter)

	slot := make(chan struct{}, 1)
	slot <- struct{}{}

	service.refresh.providers[getter.Name()] = slot

	flight, waiter, err := service.tryJoinFlight(t.Context(), getter, "AAA", nil, false)
	if err != nil {
		t.Fatal(err)
	}

	forcedCtx := refreshMetadataContext(t.Context(), true)
	joined, _, joinErr := service.tryJoinFlight(forcedCtx, getter, "AAA", nil, false)

	<-slot

	_, err = service.waitForFlight(t.Context(), flight, waiter)
	if err != nil {
		t.Fatal(err)
	}

	if joinErr == nil || joined != flight {
		t.Fatal("forced caller accepted an ordinary metadata-cache flight")
	}

	revision.Store(2)

	quote, err := service.getPrice(forcedCtx, getter.Name(), "AAA", true)
	if err != nil || quote.Value != 20 {
		t.Fatalf("forced quote=%+v, %v", quote, err)
	}
}
