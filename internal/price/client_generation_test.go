package price

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
)

func TestClientQuoteGenerationRejectsReplacedPortfolio(t *testing.T) {
	t.Parallel()

	for _, bound := range []bool{false, true} {
		name := "unbound"
		if bound {
			name = "already bound"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkClientGeneration(t, bound)
		})
	}
}

func checkClientGeneration(t *testing.T, bound bool) {
	t.Helper()

	getter := &regressionFXGetter{name: "Frankfurter", ttl: time.Minute}
	service := newRefreshTestService(t, getter)
	ctx := db.WithPortfolioGeneration(t.Context(), 0)

	var err error

	if bound {
		ctx, err = service.withQuoteGeneration(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}

	_, err = service.db.ExecContext(
		t.Context(),
		"UPDATE snapshot_state SET generation = 1 WHERE id = 1",
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.GetPrice(ctx, getter.Name(), "USD")
	if !errors.Is(err, db.ErrPortfolioChanged) {
		t.Fatalf("stale client adopted replacement generation: %v", err)
	}

	cached, err := service.getCachedPrice(getter.Name(), "USD")
	if err != nil || cached != nil || getter.calls.Load() != 0 {
		t.Fatalf(
			"stale client fetched or persisted: %+v, %v, calls=%d",
			cached,
			err,
			getter.calls.Load(),
		)
	}

	current := db.WithPortfolioGeneration(t.Context(), 1)

	quote, err := service.GetPrice(current, getter.Name(), "USD")
	if err != nil || quote.Value != 40 || getter.calls.Load() != 1 {
		t.Fatalf("current client cannot refresh: %+v, %v", quote, err)
	}
}

func TestSkippedRefreshStillChecksClientGeneration(t *testing.T) {
	t.Parallel()

	service := newRefreshTestService(t)

	for _, holdings := range [][]models.Holding{nil, {{PriceProvider: "Manual"}}} {
		err := service.RefreshAll(db.WithPortfolioGeneration(t.Context(), 1), holdings)
		if !errors.Is(err, db.ErrPortfolioChanged) {
			t.Fatalf("stale skipped refresh succeeded: %v", err)
		}

		err = service.RefreshAll(db.WithPortfolioGeneration(t.Context(), 0), holdings)
		if err != nil {
			t.Fatalf("current skipped refresh failed: %v", err)
		}
	}
}

func TestClientGenerationFencesRefreshAlreadyInFlight(t *testing.T) {
	t.Parallel()

	for _, operation := range []string{"lookup", "refresh"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			checkInFlightClientGeneration(t, operation)
		})
	}
}

func checkInFlightClientGeneration(t *testing.T, operation string) {
	t.Helper()

	getter := newGatedGetter("Generation quote")
	service := newRefreshTestService(t, getter)
	ctx := db.WithPortfolioGeneration(t.Context(), 0)
	done := make(chan error, 1)

	go func() {
		var err error

		if operation == "refresh" {
			err = service.RefreshAll(
				ctx,
				[]models.Holding{{PriceProvider: getter.Name(), InstrumentID: "TEST"}},
			)
		} else {
			_, err = service.GetPrice(ctx, getter.Name(), "TEST")
		}

		done <- err
	}()

	awaitRefreshSignal(t, getter.entered)

	_, err := service.db.ExecContext(
		t.Context(),
		"UPDATE snapshot_state SET generation = 1 WHERE id = 1",
	)
	if err != nil {
		t.Fatal(err)
	}

	getter.release <- struct{}{}

	err = awaitRefreshSignal(t, done)
	if !errors.Is(err, errQuoteSuperseded) || !errors.Is(err, db.ErrPortfolioChanged) {
		t.Fatalf("replacement must preserve superseded error and client conflict: %v", err)
	}

	cached, err := service.getCachedPrice(getter.Name(), "TEST")
	if err != nil || cached != nil {
		t.Fatalf("old quote present in replacement cache: %+v, %v", cached, err)
	}
}

func TestClientGenerationRejectsDifferentBoundQuote(t *testing.T) {
	t.Parallel()

	service := newRefreshTestService(t)
	ctx := db.WithPortfolioGeneration(t.Context(), 0)
	ctx = context.WithValue(ctx, quoteGenerationKey{}, int64(1))

	_, err := service.withQuoteGeneration(ctx)
	if !errors.Is(err, db.ErrPortfolioChanged) {
		t.Fatalf("inconsistent client/quote generations accepted: %v", err)
	}
}

func TestClientGenerationRecheckPreservesOrdinaryCancellation(t *testing.T) {
	t.Parallel()

	service := newRefreshTestService(t)
	ctx, cancel := context.WithCancel(db.WithPortfolioGeneration(t.Context(), 0))
	cancel()

	_, err := service.db.ExecContext(
		t.Context(),
		"UPDATE snapshot_state SET generation = 1 WHERE id = 1",
	)
	if err != nil {
		t.Fatal(err)
	}

	err = service.recheckClientQuoteGeneration(ctx, context.Canceled)
	if !errors.Is(err, context.Canceled) || errors.Is(err, db.ErrPortfolioChanged) {
		t.Fatalf("caller cancellation was replaced by portfolio conflict: %v", err)
	}

	stale := db.WithPortfolioGeneration(t.Context(), 0)

	err = service.recheckClientQuoteGeneration(stale, context.Canceled)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, db.ErrPortfolioChanged) {
		t.Fatalf("restore cancellation lacks client conflict: %v", err)
	}

	current := db.WithPortfolioGeneration(t.Context(), 1)

	err = service.recheckClientQuoteGeneration(current, errServiceClosed)
	if !errors.Is(err, errServiceClosed) || errors.Is(err, db.ErrPortfolioChanged) {
		t.Fatalf("ordinary shutdown was replaced by portfolio conflict: %v", err)
	}
}
