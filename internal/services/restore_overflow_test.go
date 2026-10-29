package services

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/price"
)

func TestRestoreRejectsHoldingProductOverflow(t *testing.T) {
	for _, test := range []struct {
		name     string
		mutation string
	}{
		{name: "manual valuation", mutation: `UPDATE holdings SET quantity = 1e308, manual_price = 2`},
		{name: "estimated valuation", mutation: `UPDATE holdings SET quantity = 1e308, est_price = 2`},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := openTestDB(t, "source.db")
			target := openTestDB(t, "target.db")
			seedRestoreValidationData(t, source, "Backup")
			seedRestoreValidationData(t, target, "Live")
			execRestoreFixture(t, source, test.mutation)
			// Keep this entirely offline, including the pre-fix valuation reproduction.
			_, err := source.db.ExecContext(t.Context(), `INSERT INTO price_cache
(provider, instrument_id, price, currency, source, expires_at, last_error)
VALUES ('Frankfurter', 'USD', 40, 'TRY', 'test', ?, '')`, time.Now().UTC().Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}

			prices := price.NewService(source.db, price.DefaultRegistry())

			_, err = NewHoldingService(source.db, prices).GetWithPrices(t.Context(), "TRY")
			if !errors.Is(err, errNonFiniteHoldingValue) {
				t.Fatalf(
					"reproduction: valuation error = %v; want non-finite holding valuation",
					err,
				)
			}

			path := filepath.Join(t.TempDir(), "backup.db")

			err = source.Backup(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}

			err = target.RestoreFrom(t.Context(), path)
			if !errors.Is(err, ErrIncompatibleBackup) {
				t.Fatalf(
					"RestoreFrom error = %v; want ErrIncompatibleBackup for unvaluatable holding",
					err,
				)
			}

			assertRestoreLiveData(t, target)
		})
	}
}

func TestRestoreAcceptsFiniteHoldingProducts(t *testing.T) {
	for name, mutation := range map[string]string{
		"large finite product": `UPDATE holdings SET quantity = 1e308, manual_price = 1, est_price = 1`,
		"zero quantity":        `UPDATE holdings SET quantity = 0, manual_price = 1e308, est_price = 1e308`,
		"legacy null price":    `UPDATE holdings SET quantity = 1e308, manual_price = NULL, est_price = NULL`,
	} {
		t.Run(name, func(t *testing.T) {
			source := openTestDB(t, "source.db")
			target := openTestDB(t, "target.db")
			seedRestoreValidationData(t, source, "Backup")
			execRestoreFixture(t, source, mutation)

			path := filepath.Join(t.TempDir(), "backup.db")

			err := source.Backup(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}

			err = target.RestoreFrom(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}

			if got := countRows(
				t,
				target,
				`SELECT COUNT(*) FROM holdings WHERE name = 'Backup'`,
			); got != 1 {
				t.Fatal("finite holding was not restored")
			}
		})
	}
}
