package price

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestCircuitBreakerRetriesAfterCooldown(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name         string
		instrument   string
		elapsed      time.Duration
		wantFailures int
		wantSuccess  bool
		renewed      bool
	}{
		{
			name: "recent failure", elapsed: time.Minute, instrument: "100",
			wantFailures: 3, wantSuccess: false, renewed: false,
		},
		{
			name: "recovered provider", elapsed: time.Hour, instrument: "100",
			wantFailures: 0, wantSuccess: true, renewed: false,
		},
		{
			name: "continued outage", elapsed: time.Hour, instrument: "invalid",
			wantFailures: 4, wantSuccess: false, renewed: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := newOpenBreakerService(t, test.elapsed)
			started := time.Now()

			quote, err := service.GetPrice(t.Context(), "Manual", test.instrument)
			if (err == nil) != test.wantSuccess {
				t.Fatalf("retry error = %v, wantSuccess = %v", err, test.wantSuccess)
			}

			if test.wantSuccess && quote.Value != 100 {
				t.Errorf("quote = %v, want 100", quote.Value)
			}

			service.mu.RLock()
			failures := service.failures["Manual"]
			open := service.breakers["Manual"]
			lastFail := service.lastFail["Manual"]
			service.mu.RUnlock()

			if failures != test.wantFailures || open == test.wantSuccess {
				t.Errorf("breaker state: failures=%d open=%v", failures, open)
			}

			if test.renewed && lastFail.Before(started) {
				t.Error("failed retry did not renew cooldown")
			}
		})
	}
}

func newOpenBreakerService(t *testing.T, elapsed time.Duration) *Service {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	service := NewService(database, DefaultRegistry())
	for range 3 {
		_, err = service.GetPrice(t.Context(), "Manual", "invalid")
		if err == nil {
			t.Fatal("expected provider failure")
		}
	}

	service.mu.Lock()
	service.lastFail["Manual"] = time.Now().Add(-elapsed)
	service.mu.Unlock()

	return service
}
