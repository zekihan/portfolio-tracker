package price

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestPriceCacheWaitRespectsCancellation(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })
	database.SetMaxOpenConns(1)

	connection, err := database.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	service := NewService(database, DefaultRegistry())

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	result := make(chan error, 1)

	go func() {
		_, lookupErr := service.GetPrice(ctx, "Manual", "100")
		result <- lookupErr
	}()

	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("error = %v, want deadline", err)
		}
	case <-time.After(time.Second):
		_ = connection.Close()

		<-result
		t.Fatal("price lookup ignored deadline while waiting for database connection")
	}
}
