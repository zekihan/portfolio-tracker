package price

import "strings"

// Old explicit USD-suffixed coin pairs were cached with another asset's price.
// Use their correct qualified identity for reads, refreshes, and fallback alike.
// Leave other instruments, including ambiguous raw concatenated pairs, unchanged.
func safeQuoteInstrument(provider, instrument string) string {
	if provider != "TradingView Crypto" {
		return instrument
	}

	symbol := strings.ToUpper(strings.TrimSpace(instrument))
	symbol = strings.TrimPrefix(strings.TrimPrefix(symbol, "CURRENCY:"), "CRYPTO:")

	if base, explicitPair := strings.CutSuffix(symbol, "-USD"); explicitPair &&
		strings.HasSuffix(base, currencyUSD) {
		return "CRYPTO:" + base + currencyUSD
	}

	return instrument
}
