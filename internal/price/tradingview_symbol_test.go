package price

import (
	"errors"
	"testing"
)

func TestTradingViewCryptoRejectsMissingBaseSymbol(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"", " ", "USD", "-USD", "CRYPTO:", "CRYPTO:-USD", "CURRENCY:USD"} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			getter := NewTradingViewCrypto()
			getter.client.Transport = failureBodyTransport{body: `{}`}

			_, err := getter.GetPrice(t.Context(), input)
			if !errors.Is(err, errInstrument) || !isInstrumentFailure(err) {
				t.Fatalf("missing base symbol %q returned %v", input, err)
			}
		})
	}
}
