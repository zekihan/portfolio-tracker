package models

import (
	"database/sql"
	"testing"
)

func TestEstimatedTotalValueFallsBackToCurrentOrManualPrice(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		holding Holding
		want    float64
	}{
		{name: "estimate", holding: Holding{Quantity: 2, EstPrice: 20, CurrentPrice: 10}, want: 40},
		{name: "current", holding: Holding{Quantity: 2, CurrentPrice: 10}, want: 20},
		{name: "manual override", holding: Holding{
			Quantity: 2, CurrentPrice: 10,
			ManualPrice: sql.NullFloat64{Float64: 15, Valid: true},
		}, want: 30},
		{name: "zero quantity", holding: Holding{CurrentPrice: 10}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := test.holding.EstimatedTotalValue(); got != test.want {
				t.Fatalf("estimated total = %v, want %v", got, test.want)
			}
		})
	}
}
