package price

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
)

type gatedQuoteGetter struct {
	sharedActive *atomic.Int32
	sharedPeak   *atomic.Int32
	entered      chan string
	release      chan struct{}
	exited       chan struct{}
	name         string
	calls        atomic.Int32
	active       atomic.Int32
	peak         atomic.Int32
}

func (getter *gatedQuoteGetter) Name() string       { return getter.name }
func (getter *gatedQuoteGetter) Type() string       { return stockETFType }
func (getter *gatedQuoteGetter) TTL() time.Duration { return time.Minute }

func (getter *gatedQuoteGetter) GetPrice(
	ctx context.Context,
	instrument string,
) (models.Price, error) {
	getter.calls.Add(1)

	if getter.sharedActive != nil {
		active := getter.sharedActive.Add(1)
		defer getter.sharedActive.Add(-1)

		for peak := getter.sharedPeak.Load(); active > peak; peak = getter.sharedPeak.Load() {
			if getter.sharedPeak.CompareAndSwap(peak, active) {
				break
			}
		}
	}

	active := getter.active.Add(1)
	defer getter.active.Add(-1)

	for peak := getter.peak.Load(); active > peak; peak = getter.peak.Load() {
		if getter.peak.CompareAndSwap(peak, active) {
			break
		}
	}

	getter.entered <- instrument
	defer func() { getter.exited <- struct{}{} }()

	select {
	case <-ctx.Done():
		return models.Price{}, fmt.Errorf("gated quote: %w", ctx.Err())
	case <-getter.release:
		return models.Price{
			Value:     42,
			Currency:  "USD",
			Source:    getter.name,
			FetchedAt: time.Now(),
		}, nil
	}
}

func newGatedGetter(name string) *gatedQuoteGetter {
	return &gatedQuoteGetter{
		name:    name,
		entered: make(chan string, 128),
		release: make(chan struct{}, 128),
		exited:  make(chan struct{}, 128),
	}
}

func newRefreshTestService(t *testing.T, getters ...Getter) *Service {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	registry := NewRegistry()
	for _, getter := range getters {
		registry.Register(getter)
	}

	service := NewService(database, registry)

	t.Cleanup(func() { _ = service.Close() })

	return service
}

func awaitRefreshSignal[T any](t *testing.T, signal <-chan T) T {
	t.Helper()

	select {
	case value := <-signal:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for refresh")

		var zero T

		return zero
	}
}

func seedStaleQuote(t *testing.T, service *Service, provider string) {
	t.Helper()

	quote := models.Price{
		Value:     10,
		Currency:  "USD",
		Source:    provider,
		FetchedAt: time.Now().Add(-time.Hour),
	}

	checkErr1 := service.cachePrice(provider, "TEST", &quote, -time.Minute)
	if checkErr1 != nil {
		t.Fatal(checkErr1)
	}
}

func TestExpiredPriceReturnsImmediatelyAndCoalescesRefresh(t *testing.T) {
	t.Parallel()

	getter := newGatedGetter("Gated")
	service := newRefreshTestService(t, getter)
	seedStaleQuote(t, service, getter.Name())

	for range 10 {
		quote, err := service.GetPrice(t.Context(), getter.Name(), "TEST")
		if err != nil || quote.Value != 10 || !quote.IsStale || quote.IsFailed {
			t.Fatalf("stale quote = %+v, %v", quote, err)
		}
	}

	awaitRefreshSignal(t, getter.entered)

	if getter.calls.Load() != 1 {
		t.Fatal("duplicate background fetch")
	}

	flight, _, err := service.joinFlight(t.Context(), getter, "TEST", nil, false)
	if err != nil {
		t.Fatal(err)
	}

	getter.release <- struct{}{}

	awaitRefreshSignal(t, flight.done)

	if flight.err != nil {
		t.Fatal(flight.err)
	}

	quote, err := service.GetPrice(t.Context(), getter.Name(), "TEST")
	if err != nil || quote.Value != 42 || quote.IsStale {
		t.Fatalf("fresh quote = %+v, %v", quote, err)
	}
}

func TestCoalescedCallerCancellationDoesNotCancelOtherWaiter(t *testing.T) {
	t.Parallel()

	getter := newGatedGetter("Gated")
	service := newRefreshTestService(t, getter)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	first := make(chan error, 1)

	go func() { _, err := service.GetPrice(ctx, getter.Name(), "TEST"); first <- err }()

	awaitRefreshSignal(t, getter.entered)

	second, _, err := service.joinFlight(t.Context(), getter, "TEST", nil, false)
	if err != nil {
		t.Fatal(err)
	}

	cancel()

	checkErr2 := awaitRefreshSignal(t, first)
	if !errors.Is(checkErr2, context.Canceled) {
		t.Fatalf("cancellation = %v", checkErr2)
	}

	getter.release <- struct{}{}

	awaitRefreshSignal(t, second.done)

	if second.err != nil || second.quote.Value != 42 || getter.calls.Load() != 1 {
		t.Fatalf("shared result = %+v, %v", second.quote, second.err)
	}
}

func TestRefreshConcurrencyCapsAndPendingBound(t *testing.T) {
	t.Parallel()

	var globalActive, globalPeak atomic.Int32

	entered := make(chan string, maxPendingFetches)

	getters := make([]Getter, 0, maxConcurrentFetches+1)
	for index := 0; index <= maxConcurrentFetches; index++ {
		getter := newGatedGetter(fmt.Sprintf("Provider %d", index))
		getter.entered = entered
		getter.sharedActive, getter.sharedPeak = &globalActive, &globalPeak
		getters = append(getters, getter)
	}

	service := newRefreshTestService(t, getters...)

	flights := make([]*priceFlight, 0, maxPendingFetches)
	for index := range maxPendingFetches {
		getter := getters[index%len(getters)]

		flight, _, err := service.joinFlight(t.Context(), getter, strconv.Itoa(index), nil, false)
		if err != nil {
			t.Fatal(err)
		}

		flights = append(flights, flight)
	}

	_, _, err := service.joinFlight(t.Context(), getters[0], "overflow", nil, false)
	if !errors.Is(err, errRefreshQueueFull) {
		t.Fatalf("pending limit = %v", err)
	}

	for range maxConcurrentFetches {
		awaitRefreshSignal(t, entered)
	}

	if globalActive.Load() != maxConcurrentFetches {
		t.Fatal("global refreshes did not run concurrently")
	}
	// All admitted calls remain blocked until the release below.
	if cap(service.refresh.slots) != maxConcurrentFetches {
		t.Fatal("incorrect global cap")
	}

	finishCappedFlights(t, getters, flights)

	if globalPeak.Load() != maxConcurrentFetches {
		t.Fatal("global fetch cap exceeded")
	}
}

func finishCappedFlights(t *testing.T, getters []Getter, flights []*priceFlight) {
	t.Helper()

	for _, getter := range getters {
		gated, ok := getter.(*gatedQuoteGetter)
		if !ok {
			t.Fatal("unexpected getter")
		}

		close(gated.release)
	}

	for _, flight := range flights {
		awaitRefreshSignal(t, flight.done)

		if flight.err != nil {
			t.Fatal(flight.err)
		}
	}

	for _, getter := range getters {
		gated, ok := getter.(*gatedQuoteGetter)
		if !ok || gated.peak.Load() != 1 {
			t.Fatal("per-provider fetches overlapped")
		}
	}
}

func TestCloseJoinsBackgroundRefreshAndRejectsNewWork(t *testing.T) {
	t.Parallel()

	getter := newGatedGetter("Gated")
	service := newRefreshTestService(t, getter)
	seedStaleQuote(t, service, getter.Name())

	_, checkErr3 := service.GetPrice(t.Context(), getter.Name(), "TEST")
	if checkErr3 != nil {
		t.Fatal(checkErr3)
	}

	awaitRefreshSignal(t, getter.entered)

	checkErr4 := service.Close()
	if checkErr4 != nil {
		t.Fatal(checkErr4)
	}

	awaitRefreshSignal(t, getter.exited)

	if len(service.refresh.flights) != 0 {
		t.Fatal("close retained refresh workers")
	}

	_, closedErr := service.GetPrice(t.Context(), getter.Name(), "TEST")
	if !errors.Is(closedErr, errServiceClosed) {
		t.Fatalf("closed lookup = %v", closedErr)
	}

	cached, err := service.getCachedPrice(getter.Name(), "TEST")
	if err != nil || cached.Price != 10 || cached.ErrorCount != 0 {
		t.Fatalf("canceled refresh mutated cache: %+v, %v", cached, err)
	}
}

func TestCacheResetJoinsOldGenerationBeforeReplacement(t *testing.T) {
	t.Parallel()

	for _, restore := range []bool{false, true} {
		t.Run(strconv.FormatBool(restore), func(t *testing.T) {
			t.Parallel()

			getter := newGatedGetter("Gated")
			service := newRefreshTestService(t, getter)
			seedStaleQuote(t, service, getter.Name())

			_, checkErr5 := service.GetPrice(t.Context(), getter.Name(), "TEST")
			if checkErr5 != nil {
				t.Fatal(checkErr5)
			}

			awaitRefreshSignal(t, getter.entered)

			err := service.WithCacheReset(t.Context(), func() error {
				_, deleteErr := service.db.ExecContext(
					t.Context(),
					"DELETE FROM price_cache",
				)
				if deleteErr != nil {
					return fmt.Errorf("delete cache: %w", deleteErr)
				}

				if restore {
					quote := models.Price{
						Value:     99,
						Currency:  "USD",
						Source:    getter.Name(),
						FetchedAt: time.Now(),
					}

					return service.cachePrice(getter.Name(), "TEST", &quote, time.Hour)
				}

				return nil
			})
			if err != nil {
				t.Fatal(err)
			}

			awaitRefreshSignal(t, getter.exited)

			cached, err := service.getCachedPrice(getter.Name(), "TEST")
			if err != nil {
				t.Fatal(err)
			}

			if (!restore && cached != nil) || (restore && (cached == nil || cached.Price != 99)) {
				t.Fatalf("old refresh overwrote replacement: %+v", cached)
			}
		})
	}
}

func TestResetGateCancellationAndFailedMutationPreserveHealth(t *testing.T) {
	t.Parallel()
	service := newRefreshTestService(t, NewManual())
	service.failures["Manual"] = 2
	service.refresh.gate.Lock()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, checkErr6 := service.GetPrice(ctx, "Manual", "100")
	if !errors.Is(checkErr6, context.Canceled) {
		t.Fatalf("read cancellation = %v", checkErr6)
	}

	resetErr := service.WithCacheReset(ctx, func() error {
		t.Error("canceled mutation ran")

		return nil
	})
	if !errors.Is(resetErr, context.Canceled) {
		t.Fatalf("reset cancellation = %v", resetErr)
	}
	service.refresh.gate.Unlock()

	resetErr = service.WithCacheReset(t.Context(), func() error { return errProviderUnavailable })
	if !errors.Is(resetErr, errProviderUnavailable) {
		t.Fatal(resetErr)
	}

	if service.failures["Manual"] != 2 {
		t.Fatal("failed mutation erased health")
	}
}
