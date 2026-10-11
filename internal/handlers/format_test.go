package handlers

import (
	"math"
	"testing"
)

func TestFormatNumber(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		want  string
		value float64
	}{
		{name: "nan", value: math.NaN(), want: "N/A"},
		{name: "positive infinity", value: math.Inf(1), want: "N/A"},
		{name: "negative infinity", value: math.Inf(-1), want: "N/A"},
		{name: "zero", value: 0, want: "0.00"},
		{name: "negative", value: -1234.5, want: "-1,234.50"},
		{name: "positive", value: 1234567.89, want: "1,234,567.89"},
		{name: "rounding", value: 999.999, want: "1,000.00"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := formatNumber(test.value); got != test.want {
				t.Errorf("formatNumber(%v) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}
