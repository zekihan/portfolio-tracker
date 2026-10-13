package price

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
)

func TestDefaultProviderCacheLifetimes(t *testing.T) {
	service := newRefreshTestService(t)
	for _, getter := range DefaultRegistry().All() {
		t.Run(getter.Name(), func(t *testing.T) {
			want := 15 * time.Minute
			if getter.Name() == "Manual" {
				want = 0
			}

			if got := getter.TTL(); got != want {
				t.Fatalf("TTL = %s, want %s", got, want)
			}

			var storedTTL int

			err := service.db.GetContext(t.Context(), &storedTTL,
				"SELECT ttl_seconds FROM provider_registry WHERE name = ?", getter.Name())
			if err != nil {
				t.Fatal(err)
			}

			if storedTTL != int(want.Seconds()) {
				t.Errorf("registry TTL = %d, want %d", storedTTL, int(want.Seconds()))
			}

			quote := models.Price{Value: 42, Currency: "TRY", FetchedAt: time.Now()}
			before := time.Now().Truncate(time.Second)

			err = service.cachePrice(getter.Name(), "TTL", &quote, getter.TTL())
			if err != nil {
				t.Fatal(err)
			}

			cached, err := service.getCachedPriceContext(t.Context(), getter.Name(), "TTL")
			if err != nil {
				t.Fatal(err)
			}

			if want == 0 {
				want = 24 * time.Hour
			}

			if cached.ExpiresAt.Before(before.Add(want)) ||
				cached.ExpiresAt.After(time.Now().Add(want)) {
				t.Errorf("cache expiry = %s, want %s after write", cached.ExpiresAt, want)
			}
		})
	}
}

func checkFundMetadataTTL(t *testing.T, getter Getter) {
	t.Helper()

	var expiries []time.Time

	switch provider := getter.(type) {
	case *PusulaPortfoy:
		provider.funds.mu.Lock()
		expiries = append(expiries, provider.funds.expires)
		provider.funds.mu.Unlock()
	case *GarantiBBVA:
		provider.date.mu.Lock()
		expiries = append(expiries, provider.date.expires)
		provider.date.mu.Unlock()
		provider.currencies.mu.Lock()
		expiries = append(expiries, provider.currencies.expires)
		provider.currencies.mu.Unlock()
	default:
		t.Fatalf("unsupported fund provider %T", getter)
	}

	for _, expiry := range expiries {
		remaining := time.Until(expiry)
		if remaining < 14*time.Minute || remaining > 15*time.Minute {
			t.Errorf("metadata cache remaining TTL = %s, want approximately 15m", remaining)
		}
	}
}

func TestProviderTTLMigratesExistingDatabase(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "legacy.db")

	legacy, err := sqlx.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = legacy.Close() })

	for _, migration := range db.Migrations[:8] {
		_, err = legacy.ExecContext(t.Context(), migration)
		if err != nil {
			t.Fatal(err)
		}
	}

	_, err = legacy.ExecContext(t.Context(), `PRAGMA user_version = 8;
		UPDATE provider_registry SET enabled = 0, key_env_var = 'CUSTOM_KEY' WHERE name = 'Yahoo Finance';
		INSERT INTO provider_registry (name, type, ttl_seconds) VALUES ('Custom', 'FX', 123);`)
	if err != nil {
		t.Fatal(err)
	}

	err = legacy.Close()
	if err != nil {
		t.Fatal(err)
	}

	migrated, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = migrated.Close() })

	for _, getter := range DefaultRegistry().All() {
		var ttl int

		err = migrated.GetContext(t.Context(), &ttl,
			"SELECT ttl_seconds FROM provider_registry WHERE name = ?", getter.Name())
		if err != nil {
			t.Fatal(err)
		}

		if ttl != int(getter.TTL().Seconds()) {
			t.Errorf("%s migrated TTL = %d, want %s", getter.Name(), ttl, getter.TTL())
		}
	}

	var preserved int

	err = migrated.GetContext(t.Context(), &preserved, `SELECT COUNT(*) FROM provider_registry
		WHERE (name = 'Yahoo Finance' AND enabled = 0 AND key_env_var = 'CUSTOM_KEY')
		OR (name = 'Custom' AND ttl_seconds = 123)`)
	if err != nil {
		t.Fatal(err)
	}

	if preserved != 2 {
		t.Errorf("preserved registry configurations = %d, want 2", preserved)
	}
}
