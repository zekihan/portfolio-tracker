package price

import (
	"errors"
	"net/http"
)

var errProviderRedirectDenied = errors.New("provider redirects are not allowed")

// Provider endpoints are fixed HTTPS URLs. Reject every redirect before a
// second request can disclose credentials or reach an untrusted destination.
func newProviderClient() *http.Client {
	return &http.Client{
		Timeout: providerTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errProviderRedirectDenied
		},
	}
}
