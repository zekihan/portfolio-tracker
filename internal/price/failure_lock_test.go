package price

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestFailurePersistenceDoesNotBlockHealthLock(t *testing.T) {
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
	finished := make(chan struct{})

	go func() {
		service.recordFailure("Yahoo Finance", "TEST", errProviderUnavailable)
		close(finished)
	}()

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()

	for database.Stats().WaitCount == 0 {
		select {
		case <-ticker.C:
		case <-timeout.C:
			_ = connection.Close()

			<-finished
			t.Fatal("failure persistence did not reach database wait")
		}
	}

	health := make(chan int, 1)

	go func() {
		service.mu.RLock()
		failures := service.failures["Yahoo Finance"]
		service.mu.RUnlock()

		health <- failures
	}()

	select {
	case failures := <-health:
		if failures != 1 {
			t.Errorf("failures = %d, want 1", failures)
		}
	case <-time.After(time.Second):
		_ = connection.Close()

		<-finished
		<-health
		t.Fatal("database wait blocked provider health lock")
	}

	_ = connection.Close()

	<-finished
}
