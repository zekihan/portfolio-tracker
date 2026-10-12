package price

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

type panicQuoteGetter struct{ Getter }

func (getter *panicQuoteGetter) GetPrice(context.Context, string) (models.Price, error) {
	panic("test provider panic")
}

func TestSharedFetchContainsProviderPanic(t *testing.T) {
	t.Parallel()

	for _, cached := range []bool{false, true} {
		t.Run(strconv.FormatBool(cached), func(t *testing.T) {
			t.Parallel()

			getter := &panicQuoteGetter{Getter: NewManual()}

			service := newRefreshTestService(t, getter)
			if cached {
				seedStaleQuote(t, service, getter.Name())
			}

			quote, err := service.getPrice(t.Context(), getter.Name(), "TEST", true)
			if cached {
				if err != nil || !quote.IsFailed || !quote.IsStale ||
					!errors.Is(quote.Error, errProviderPanicked) {
					t.Fatalf("panic fallback = %+v, %v", quote, err)
				}
			} else if !errors.Is(err, errProviderPanicked) {
				t.Fatalf("cold panic = %v", err)
			}
		})
	}
}

type cancelCleanupGetter struct {
	started  chan struct{}
	canceled chan struct{}
	cleanup  chan struct{}
	calls    atomic.Int32
}

func (getter *cancelCleanupGetter) Name() string       { return "Cancel cleanup" }
func (getter *cancelCleanupGetter) Type() string       { return stockETFType }
func (getter *cancelCleanupGetter) TTL() time.Duration { return time.Minute }
func (getter *cancelCleanupGetter) GetPrice(ctx context.Context, _ string) (models.Price, error) {
	if getter.calls.Add(1) == 1 {
		close(getter.started)
		<-ctx.Done()
		close(getter.canceled)
		<-getter.cleanup

		return models.Price{}, fmt.Errorf("canceled quote: %w", ctx.Err())
	}

	return models.Price{
		Value:     77,
		Currency:  "USD",
		Source:    getter.Name(),
		FetchedAt: time.Now(),
	}, nil
}

func TestNewCallerWaitsForAbandonedFlightThenRetries(t *testing.T) {
	t.Parallel()

	getter := &cancelCleanupGetter{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
		cleanup:  make(chan struct{}),
	}
	service := newRefreshTestService(t, getter)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	first := make(chan error, 1)

	go func() { _, err := service.GetPrice(ctx, getter.Name(), "TEST"); first <- err }()

	awaitRefreshSignal(t, getter.started)
	cancel()
	awaitRefreshSignal(t, getter.canceled)

	_, _, abandonedErr := service.tryJoinFlight(t.Context(), getter, "TEST", nil, false)
	if !errors.Is(abandonedErr, errAbandonedFlight) {
		t.Fatalf("old flight not detected as abandoned: %v", abandonedErr)
	}

	second := make(chan error, 1)
	// Exercise admission while the old canceled flight is still present.
	go func() {
		quote, err := service.GetPrice(t.Context(), getter.Name(), "TEST")
		if err == nil && quote.Value != 77 {
			err = fmt.Errorf("%w: missing replacement quote", errRefresh)
		}

		second <- err
	}()

	close(getter.cleanup)

	firstErr := awaitRefreshSignal(t, first)
	if !errors.Is(firstErr, context.Canceled) {
		t.Fatalf("first waiter = %v", firstErr)
	}

	secondErr := awaitRefreshSignal(t, second)
	if secondErr != nil {
		t.Fatalf("new waiter inherited cancellation: %v", secondErr)
	}

	if getter.calls.Load() != 2 {
		t.Fatal("replacement fetch not performed")
	}
}

func TestPanickingResetDoesNotPoisonNextGeneration(t *testing.T) {
	t.Parallel()
	service := newRefreshTestService(t, NewManual())

	func() {
		defer func() {
			if recover() == nil {
				t.Error("reset panic was swallowed")
			}
		}()

		err := service.WithCacheReset(t.Context(), func() error { panic("reset failed") })
		if err != nil {
			t.Error(err)
		}
	}()

	quote, err := service.GetPrice(t.Context(), "Manual", "100")
	if err != nil || quote.Value != 100 {
		t.Fatalf("reset poisoned future requests: %+v, %v", quote, err)
	}
}

func TestProviderProbeSharesFetchLimitAndClose(t *testing.T) {
	t.Parallel()

	getter := newGatedGetter("Gated")
	service := newRefreshTestService(t, getter)

	flight, _, err := service.joinFlight(t.Context(), getter, "FIRST", nil, false)
	if err != nil {
		t.Fatal(err)
	}

	awaitRefreshSignal(t, getter.entered)

	probeResult := make(chan error, 1)

	go func() {
		_, probeErr := service.TestProvider(t.Context(), getter.Name(), "SECOND")
		probeResult <- probeErr
	}()

	getter.release <- struct{}{}

	awaitRefreshSignal(t, flight.done)
	awaitRefreshSignal(t, getter.entered)

	if getter.peak.Load() != 1 {
		t.Fatal("probe bypassed provider concurrency cap")
	}

	err = service.Close()
	if err != nil {
		t.Fatal(err)
	}

	probeErr := awaitRefreshSignal(t, probeResult)
	if !errors.Is(probeErr, context.Canceled) {
		t.Fatalf("probe close = %v", probeErr)
	}

	cached, err := service.getCachedPrice(getter.Name(), "SECOND")
	if err != nil || cached != nil {
		t.Fatalf("probe changed cache: %+v %v", cached, err)
	}
}
