package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

func TestEstimatedTotalPagesAndAPIUseCurrentPriceFallback(t *testing.T) {
	t.Chdir("../..")

	for _, test := range []struct {
		name      string
		estimates [2]float64
		wantTRY   float64
		partial   bool
	}{
		{name: "no estimates", wantTRY: 1400},
		{name: "mixed", estimates: [2]float64{200, 0}, wantTRY: 1600},
		{name: "all estimates", estimates: [2]float64{200, 20}, wantTRY: 2800},
		{name: "missing quote and zero quantity", estimates: [2]float64{200, 0}, wantTRY: 1600, partial: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := estimatedTotalRouter(t, test.estimates, test.partial)

			for _, currency := range []string{"TRY", "USD", "EUR"} {
				rate := map[string]float64{"TRY": 1, "USD": 40, "EUR": 50}[currency]
				body := getEstimatedTotalPage(t, router, "/?currency="+currency)

				want := `id="estimated-value-try"> ` + formatCurrency(
					test.wantTRY/rate,
					currency,
				) + " </div>"
				if !strings.Contains(body, want) ||
					!strings.Contains(body, "otherwise current prices") {
					t.Errorf("%s dashboard missing effective estimate %q", currency, want)
				}

				checkEstimatedComparison(t, body, currency, (test.wantTRY-1400)/rate, test.partial)

				body = getEstimatedTotalPage(t, router, "/holdings?currency="+currency)

				secondTotal := float64(1200)
				if test.estimates[1] > 0 {
					secondTotal = 2400
				}
				// Check both the desktop estimate cell and mobile estimate line.
				formatted := formatCurrency(secondTotal/rate, currency)
				if !strings.Contains(body, `class="numeric calculated-cell">`+formatted+"</td>") ||
					!strings.Contains(body, `Est: `+formatted) {
					t.Errorf(
						"%s desktop/mobile missing estimated contribution %q",
						currency,
						formatted,
					)
				}

				if test.partial &&
					(!strings.Contains(body, "Estimated total covers 3 of 4 holdings") ||
						!strings.Contains(body, "Est: Unavailable") || !strings.Contains(body, "Est: "+formatCurrency(0, currency))) {
					t.Error(
						"missing quote must be unavailable while zero quantity remains a known zero",
					)
				}

				checkEstimatedTotalAPI(t, router, currency, test.wantTRY, test.partial)
			}
		})
	}
}

func checkEstimatedComparison(
	t *testing.T,
	body, currency string,
	difference float64,
	partial bool,
) {
	t.Helper()

	if partial {
		if !strings.Contains(body, "Partial Estimated Value") ||
			!strings.Contains(body, "3 of 4 holdings included; remaining values unavailable") ||
			strings.Contains(body, "Diff:") {
			t.Error(
				"missing-quote estimated subtotal should be explicitly partial without comparison",
			)
		}
	} else if !strings.Contains(body, "Diff: "+formatCurrency(difference, currency)) ||
		strings.Contains(body, "Partial Estimated Value") || strings.Contains(body, "add prices") {
		t.Error("full effective estimate should allow comparison even with unset estimates")
	}
}

func getEstimatedTotalPage(t *testing.T, router http.Handler, path string) string {
	t.Helper()

	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil),
	)

	if response.Code != http.StatusOK {
		t.Fatalf("%s returned %d: %s", path, response.Code, response.Body)
	}

	return strings.Join(strings.Fields(response.Body.String()), " ")
}

func checkEstimatedTotalAPI(
	t *testing.T,
	router http.Handler,
	currency string,
	wantTRY float64,
	partial bool,
) {
	t.Helper()
	body := getEstimatedTotalPage(t, router, "/api/dashboard?currency="+currency)

	var data models.DashboardData
	//nolint:musttag // DashboardData defines the existing public JSON response without tags.
	err := json.Unmarshal([]byte(body), &data)
	if err != nil {
		t.Fatal(err)
	}

	if data.EstimatedValueTRY != wantTRY || data.EstimatedValueUSD != wantTRY/40 ||
		data.EstimatedValueEUR != wantTRY/50 || data.EstimatedValuationComplete == partial {
		t.Fatalf("API estimated total disagrees with page: %+v", data)
	}
}

func estimatedTotalRouter(t *testing.T, estimates [2]float64, partial bool) http.Handler {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
(name,asset_type,base_currency,quantity,manual_price,est_price) VALUES
('Lira','Cash','TRY',2,100,?), ('Dollar','Cash','USD',3,10,?)`, estimates[0], estimates[1])
	if err != nil {
		t.Fatal(err)
	}

	_, err = database.ExecContext(t.Context(), `INSERT INTO price_cache
(provider,instrument_id,price,currency,source,expires_at,last_error) VALUES
('Frankfurter','USD',40,'TRY','test',?,''), ('Frankfurter','EUR',50,'TRY','test',?,'')`,
		time.Now().Add(time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	if partial {
		_, err = database.ExecContext(t.Context(), `INSERT INTO holdings
(name,asset_type,base_currency,quantity,manual_price,est_price) VALUES
('Missing','Cash','TRY',1,0,NULL), ('Closed','Cash','TRY',0,0,NULL)`)
		if err != nil {
			t.Fatal(err)
		}
	}

	prices := price.NewService(database, price.DefaultRegistry())

	t.Cleanup(func() { _ = prices.Close() })

	holdings := services.NewHoldingService(database, prices)

	return New(holdings, services.NewSnapshotService(database, prices, holdings),
		services.NewSettingsService(database), prices).SetupRoutes()
}
