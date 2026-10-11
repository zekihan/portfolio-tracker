package services

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

func dropTestProviderRegistry(t *testing.T, database *sqlx.DB) {
	t.Helper()

	_, err := database.ExecContext(t.Context(), "DROP TABLE provider_registry")
	if err != nil {
		t.Fatal(err)
	}
}

func TestDashboardPropagatesProviderHealthFailures(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		broken bool
	}{
		{name: "valid", broken: false},
		{name: "missing provider registry", broken: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			_, err = database.ExecContext(t.Context(), `INSERT INTO price_cache
    (provider, instrument_id, price, currency, source, expires_at, last_error)
    VALUES ('Frankfurter', 'USD', 40, 'TRY', 'test', ?, '')`, time.Now().UTC().Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}

			if test.broken {
				dropTestProviderRegistry(t, database)
			}

			prices := price.NewService(database, price.DefaultRegistry())
			service := NewHoldingService(database, prices)

			dashboard, err := service.GetDashboardData(t.Context(), "TRY")
			if test.broken {
				if err == nil {
					t.Error("suppressed provider health database error")
				}

				if dashboard != nil {
					t.Error("returned misleading dashboard")
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if dashboard == nil {
				t.Fatal("missing valid dashboard")
			}
		})
	}
}
