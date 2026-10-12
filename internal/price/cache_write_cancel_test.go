package price

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
)

type poolBlockingGetter struct {
	database *sqlx.DB
	acquired chan *sql.Conn
}

func (getter *poolBlockingGetter) Name() string       { return "Pool blocking" }
func (getter *poolBlockingGetter) Type() string       { return "Stock/ETF" }
func (getter *poolBlockingGetter) TTL() time.Duration { return time.Minute }
func (getter *poolBlockingGetter) GetPrice(ctx context.Context, _ string) (models.Price, error) {
	connection, err := getter.database.Conn(ctx)
	if err != nil {
		return models.Price{}, fmt.Errorf("reserve provider connection: %w", err)
	}

	getter.acquired <- connection

	return models.Price{
		Value: 100, Currency: "USD", Source: getter.Name(), FetchedAt: time.Now(),
		Error: nil, IsStale: false, IsFailed: false,
	}, nil
}

func TestPriceCacheWriteRespectsCancellation(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })
	database.SetMaxOpenConns(1)
	getter := &poolBlockingGetter{database: database, acquired: make(chan *sql.Conn, 1)}
	registry := NewRegistry()
	registry.Register(getter)
	service := NewService(database, registry)
	service.failures[getter.Name()] = 2

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	result := make(chan error, 1)

	go func() {
		_, lookupErr := service.GetPrice(ctx, getter.Name(), "TEST")
		result <- lookupErr
	}()

	var connection *sql.Conn
	select {
	case connection = <-getter.acquired:
	case lookupErr := <-result:
		t.Fatalf("lookup ended before provider acquired connection: %v", lookupErr)
	}

	defer connection.Close()

	// The provider owns the only DB connection, so the quote cannot be persisted.
	cancel()

	select {
	case lookupErr := <-result:
		if !errors.Is(lookupErr, context.Canceled) {
			t.Errorf("error = %v, want cancellation", lookupErr)
		}
	case <-time.After(time.Second):
		_ = connection.Close()

		<-result
		t.Fatal("cache persistence ignored request deadline")
	}

	_ = connection.Close()

	if service.failures[getter.Name()] != 2 {
		t.Error("expired request reset provider failures")
	}

	cached, err := service.getCachedPrice(getter.Name(), "TEST")
	if err != nil {
		t.Fatal(err)
	}

	if cached != nil {
		t.Error("expired request persisted a quote")
	}
}
