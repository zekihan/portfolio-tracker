package services

import (
	"path/filepath"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestClearAllDataRemovesUserDataAndResetsSettings(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	ctx := t.Context()

	seed := []string{
		`INSERT INTO holdings (name, asset_type) VALUES ('Stock', 'Stock')`,
		`INSERT INTO snapshots (total_value_try, total_value_usd, usd_try_rate, holdings_json)
			VALUES (1, 1, 1, '[]')`,
		`INSERT INTO price_cache (provider, instrument_id, price, currency, source, expires_at)
			VALUES ('Yahoo Finance', 'AAPL', 1, 'USD', 'test', CURRENT_TIMESTAMP)`,
		`UPDATE settings SET value = 'dark' WHERE key = 'dark_mode'`,
		`UPDATE settings SET value = 'USD' WHERE key = 'display_currency'`,
	}
	for _, statement := range seed {
		_, execErr := database.ExecContext(ctx, statement)
		if execErr != nil {
			t.Fatal(execErr)
		}
	}

	service := NewSettingsService(database)

	clearErr := service.ClearAllData(ctx)
	if clearErr != nil {
		t.Fatal(clearErr)
	}

	for _, table := range []string{"holdings", "snapshots", "price_cache"} {
		var count int

		countErr := database.GetContext(ctx, &count, "SELECT COUNT(*) FROM "+table)
		if countErr != nil {
			t.Fatal(countErr)
		}

		if count != 0 {
			t.Errorf("%s has %d rows after clear, want 0", table, count)
		}
	}

	var registryCount int

	countErr := database.GetContext(ctx, &registryCount, "SELECT COUNT(*) FROM provider_registry")
	if countErr != nil {
		t.Fatal(countErr)
	}

	if registryCount == 0 {
		t.Error("provider_registry was cleared, want provider configuration kept")
	}

	settings, err := service.GetAll(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if settings.DisplayCurrency != "TRY" || settings.DarkMode != "system" ||
		settings.RefreshInterval != 60 {
		t.Errorf("settings after clear = %+v, want defaults", settings)
	}
}
