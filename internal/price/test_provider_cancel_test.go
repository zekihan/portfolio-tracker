package price

import (
	"context"
	"errors"
	"testing"
)

func TestProviderTestRespectsCancellation(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		before bool
		during bool
	}{
		{name: "live manual", before: false, during: false},
		{name: "canceled manual", before: true, during: false},
		{name: "canceled successful fetch", before: false, during: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			registry := DefaultRegistry()
			provider, instrument := "Manual", "100"

			if test.during {
				getter := &cancelingSuccessGetter{cancel: cancel}
				registry.Register(getter)
				provider = getter.Name()
			}

			if test.before {
				cancel()
			}

			service := NewService(nil, registry)

			quote, err := service.TestProvider(ctx, provider, instrument)
			if test.before || test.during {
				if !errors.Is(err, context.Canceled) {
					t.Errorf("error = %v, want cancellation", err)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if quote.Value != 100 {
				t.Errorf("price = %v, want 100", quote.Value)
			}
		})
	}
}
