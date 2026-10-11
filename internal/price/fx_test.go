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
