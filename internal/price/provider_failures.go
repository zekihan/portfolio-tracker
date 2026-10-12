package price

import (
	"errors"
	"fmt"
	"io"
	"net/http"
)

// Limit decompressed response data, including chunked responses with no length.
// Fund metadata HTML is larger than a quote, so leave reasonable headroom.
const maxProviderResponseBytes = 4 << 20

var errProviderResponseTooLarge = errors.New("provider response exceeds size limit")

var errMalformedProviderResponse = errors.New("malformed provider response")

func readProviderBody(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxProviderResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if len(data) > maxProviderResponseBytes {
		return nil, errProviderResponseTooLarge
	}

	return data, nil
}

type instrumentError struct{ cause error }

func (err *instrumentError) Error() string { return err.cause.Error() }
func (err *instrumentError) Unwrap() error { return err.cause }

func instrumentFailure(err error) error { return &instrumentError{cause: err} }

// These statuses are request-specific on the single-instrument quote endpoints.
// Authentication, quota/rate limits (429), server failures and unknown statuses
// remain provider-wide: retrying every holding would compound those failures.
func providerHTTPError(cause error, status int) error {
	err := fmt.Errorf("%w %d", cause, status)
	switch status {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity:
		return instrumentFailure(err)
	default:
		return err
	}
}

// Only explicit quote-level failures bypass the shared breaker. Unknown errors,
// including malformed responses and transport failures, remain provider-wide.
func isInstrumentFailure(err error) bool {
	instrumentErr, ok := errors.AsType[*instrumentError](err)
	if ok && instrumentErr != nil {
		return true
	}

	for _, cause := range []error{
		errNoDataFor, errInvalidMarketPriceFor, errNoPriceDataFor,
		errInvalidAlphaVantagePriceFor, errInvalidTwelveDataPriceFor,
		errFund, errGoldInstrument, errInstrument, errQuoteCurrencyForFund,
		errUnexpectedQuoteCurrency, errProviderReturnedAnInvalidPrice,
		errInvalidUSDPriceFor, errInvalidTRYRateFor, errNoRateFor, errInvalidManualPrice,
	} {
		if errors.Is(err, cause) {
			return true
		}
	}

	return false
}
