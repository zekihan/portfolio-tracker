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

func TestFormatQuantityPreservesFractionalUnits(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		want  string
		value float64
	}{
		{value: 0.00000001, want: "0.00000001"},
		{value: 12.345678, want: "12.345678"},
		{value: 100, want: "100"},
		{value: 0, want: "0"},
	} {
		if got := formatQuantity(test.value); got != test.want {
			t.Errorf("formatQuantity(%v) = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestFormatCurrencyIncludesEuro(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ currency, want string }{
		{"TRY", "₺ 1,234.50"}, {"USD", "$ 1,234.50"}, {"EUR", "€ 1,234.50"},
	} {
		if got := formatCurrency(1234.5, test.currency); got != test.want {
			t.Errorf("formatCurrency(%s) = %q, want %q", test.currency, got, test.want)
		}
	}
}
