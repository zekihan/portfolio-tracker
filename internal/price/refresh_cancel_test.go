package price

import (
	"context"
	"errors"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

func TestRefreshAllRespectsCancellationForSkippedHoldings(t *testing.T) {
	t.Parallel()

	var manual, unconfigured, missingInstrument models.Holding

	manual.PriceProvider = "Manual"
	missingInstrument.PriceProvider = "Yahoo Finance"

	for _, test := range []struct {
		name     string
		holdings []models.Holding
	}{
		{name: "empty", holdings: nil},
		{name: "manual", holdings: []models.Holding{manual}},
		{name: "unconfigured", holdings: []models.Holding{unconfigured}},
		{name: "missing instrument", holdings: []models.Holding{missingInstrument}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			service := NewService(nil, DefaultRegistry())
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			err := service.RefreshAll(ctx, test.holdings)
			if !errors.Is(err, context.Canceled) {
				t.Errorf("canceled refresh error = %v, want context.Canceled", err)
			}

			err = service.RefreshAll(t.Context(), test.holdings)
			if err != nil {
				t.Fatalf("live skipped refresh failed: %v", err)
			}
		})
	}
}
