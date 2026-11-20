package price

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestFXRateRejectsInvalidPrices(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		primary  float64
		fallback float64
		want     float64
		wantErr  bool
	}{
		{name: "zero", primary: 0, fallback: 40, want: 40, wantErr: false},
		{name: "negative", primary: -1, fallback: 40, want: 40, wantErr: false},
		{name: "infinite", primary: math.Inf(1), fallback: 40, want: 40, wantErr: false},
		{name: "both invalid", primary: 0, fallback: 0, want: 0, wantErr: true},
		{name: "valid primary", primary: 30, fallback: 40, want: 30, wantErr: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			_, err = database.ExecContext(t.Context(), `
				INSERT INTO price_cache
				(provider, instrument_id, price, currency, source, expires_at, last_error)
				VALUES ('Frankfurter', 'USD', ?, 'TRY', 'test', ?, ''),
				('Yahoo Finance', 'USDTRY=X', ?, 'TRY', 'test', ?, '')
			`, test.primary, time.Now().UTC().Add(time.Hour),
				test.fallback, time.Now().UTC().Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}

			service := NewService(database, DefaultRegistry())

			rate, err := service.GetUSDTTRYRate(t.Context())
			if (err != nil) != test.wantErr {
				t.Errorf("error = %v, want error: %v", err, test.wantErr)
			}

			if rate != test.want {
				t.Errorf("FX rate = %v, want %v", rate, test.want)
			}
		})
	}
}

func TestCachedFXRatesRespectSafetyBounds(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		primary  float64
		fallback float64
		want     float64
	}{
		{name: "primary below minimum", primary: minFXRate / 2, fallback: 40, want: 40},
		{name: "primary above maximum", primary: maxProviderQuote * 2, fallback: 40, want: 40},
		{name: "both out of bounds", primary: minFXRate / 2, fallback: maxProviderQuote * 2},
		{name: "minimum accepted", primary: minFXRate, fallback: 40, want: minFXRate},
		{name: "maximum accepted", primary: maxProviderQuote, fallback: 40, want: maxProviderQuote},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			primary := &regressionFXGetter{name: "Frankfurter", ttl: time.Minute}
			fallback := &regressionFXGetter{name: "Yahoo Finance", ttl: time.Minute}
			service := newRefreshTestService(t, primary, fallback)
			ctx := WithCachedQuotesOnly(t.Context())

			for _, currency := range []string{"USD", "EUR"} {
				seedRecoveryFX(t, service, primary.Name(), currency, test.primary, 0)
				seedRecoveryFX(t, service, fallback.Name(), currency+"TRY=X", test.fallback, 0)

				quote, err := service.getFXTRYQuote(ctx, currency, false)
				if quote.Value != test.want || (err != nil) != (test.want == 0) {
					t.Errorf("%s/TRY rate = %g, %v; want %g", currency, quote.Value, err, test.want)
				}
			}
		})
	}
}
