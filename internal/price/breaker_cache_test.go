package price

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

// A fresh cache is still a successful quote while the breaker is open.
// An expired cache keeps its value but is a failed stale read, and the open
// breaker must not call the provider. A closed breaker may refresh in the background.
func TestFreshCacheAndOpenBreakerStayDistinct(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		ttl        time.Duration
		open       bool
		wantStale  bool
		wantFailed bool
		wantCalls  int32
	}{
		{name: "fresh cache, breaker open", ttl: time.Hour, open: true},
		{
			name: "stale cache, breaker open", ttl: -time.Minute, open: true,
			wantStale: true, wantFailed: true,
		},
		{name: "stale cache, breaker closed", ttl: -time.Minute, wantStale: true, wantCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			getter := &countingQuoteGetter{value: 25, called: make(chan struct{}, 1)}
			service := newRefreshTestService(t, getter)

			err := service.cachePrice(getter.Name(), "TEST", &models.Price{
				Value: 10, Currency: "USD", Source: getter.Name(), FetchedAt: time.Now().UTC(),
			}, test.ttl)
			if err != nil {
				t.Fatal(err)
			}

			if test.open {
				service.mu.Lock()
				service.breakers[getter.Name()] = true
				service.lastFail[getter.Name()] = time.Now()
				service.mu.Unlock()
			}

			quote, err := service.GetPrice(t.Context(), getter.Name(), "TEST")
			if err != nil || quote.Value != 10 || quote.Currency != "USD" ||
				quote.IsStale != test.wantStale || quote.IsFailed != test.wantFailed {
				t.Fatalf("quote = %+v, %v", quote, err)
			}

			service.refresh.mu.Lock()
			pending := len(service.refresh.flights)
			service.refresh.mu.Unlock()

			if test.wantCalls == 0 {
				if getter.calls.Load() != 0 || pending != 0 {
					t.Fatalf("open breaker called the provider: calls=%d pending=%d",
						getter.calls.Load(), pending)
				}

				return
			}

			awaitRefreshSignal(t, getter.called)

			if getter.calls.Load() != test.wantCalls || quote.Value == getter.value {
				t.Fatalf("stale read waited for a fresh quote: calls=%d quote=%v",
					getter.calls.Load(), quote.Value)
			}
		})
	}
}

type countingQuoteGetter struct {
	called chan struct{}
	calls  atomic.Int32
	value  float64
}

func (getter *countingQuoteGetter) Name() string       { return "Counting" }
func (getter *countingQuoteGetter) Type() string       { return "Stock" }
func (getter *countingQuoteGetter) TTL() time.Duration { return time.Minute }
func (getter *countingQuoteGetter) GetPrice(context.Context, string) (models.Price, error) {
	getter.calls.Add(1)

	select {
	case getter.called <- struct{}{}:
	default:
	}

	return models.Price{
		Value: getter.value, Currency: "USD", Source: getter.Name(), FetchedAt: time.Now().UTC(),
	}, nil
}
