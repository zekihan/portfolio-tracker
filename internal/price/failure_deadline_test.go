package price

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
)

type poolFailingGetter struct{ *poolBlockingGetter }

func (getter *poolFailingGetter) GetPrice(
	ctx context.Context,
	instrument string,
) (models.Price, error) {
	quote, err := getter.poolBlockingGetter.GetPrice(ctx, instrument)
	if err != nil {
		return models.Price{}, err
	}

	return quote, errProviderUnavailable
}

func TestFailurePersistenceRespectsDeadline(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })
	database.SetMaxOpenConns(1)
	getter := &poolFailingGetter{poolBlockingGetter: &poolBlockingGetter{
		database: database, acquired: make(chan *sql.Conn, 1),
	}}

	_, err = database.ExecContext(t.Context(), `INSERT INTO price_cache
  (provider, instrument_id, price, currency, source, expires_at, last_error)
  VALUES (?, 'TEST', 100, 'USD', 'test', ?, '')`, getter.Name(), time.Now().UTC().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry()
	registry.Register(getter)
	service := NewService(database, registry)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	result := make(chan error, 1)

	go func() {
		_, lookupErr := service.getPrice(ctx, getter.Name(), "TEST", true)
		result <- lookupErr
	}()

	var connection *sql.Conn
	select {
	case connection = <-getter.acquired:
	case lookupErr := <-result:
		t.Fatalf("lookup ended before provider acquired connection: %v", lookupErr)
	}

	defer connection.Close()

	select {
	case lookupErr := <-result:
		if !errors.Is(lookupErr, context.DeadlineExceeded) {
			t.Errorf("error = %v, want deadline", lookupErr)
		}
	case <-time.After(time.Second):
		_ = connection.Close()

		<-result
		t.Fatal("failure persistence ignored request deadline")
	}

	_ = connection.Close()

	var failures int

	err = database.GetContext(
		t.Context(),
		&failures,
		"SELECT error_count FROM price_cache WHERE instrument_id = 'TEST'",
	)
	if err != nil {
		t.Fatal(err)
	}

	if failures != 0 {
		t.Error("expired request persisted failure metadata")
	}
}
