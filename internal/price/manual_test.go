package price

import "testing"

func TestManualRejectsInvalidPrices(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"", "invalid", "12oops", "NaN", "+Inf", "-Inf", "-1"} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			quote, err := NewManual().GetPrice(t.Context(), input)
			if err == nil {
				t.Errorf("accepted invalid price %q: %+v", input, quote)
			}
		})
	}
}

func TestManualValidPrices(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		input string
		value float64
	}{
		{input: "0", value: 0},
		{input: "12.50", value: 12.5},
		{input: "1e2", value: 100},
	} {
		t.Run(test.input, func(t *testing.T) {
			t.Parallel()

			quote, err := NewManual().GetPrice(t.Context(), test.input)
			if err != nil {
				t.Fatal(err)
			}

			if quote.Value != test.value || quote.Currency != "TRY" || quote.Source != "Manual" {
				t.Errorf("unexpected quote: %+v", quote)
			}
		})
	}
}
