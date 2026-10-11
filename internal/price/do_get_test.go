package price

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestDoGetReturnsErrorForMalformedURL(t *testing.T) {
	t.Parallel()

	resp, err := doGet(t.Context(), http.DefaultClient, "http://[::1", nil)
	if err == nil {
		if resp != nil {
			_ = resp.Body.Close()
		}

		t.Fatal("doGet() error = nil, want error for malformed URL")
	}

	if resp != nil {
		t.Fatal("doGet() returned a response alongside a request-construction error")
	}
}

func TestDoGetErrorsDoNotLeakQueryString(t *testing.T) {
	t.Parallel()

	// Port 1 refuses connections, so the transport error includes the request URL.
	const target = "http://127.0.0.1:1/query?function=GLOBAL_QUOTE&apikey=SUPERSECRETKEY"

	resp, err := doGet(t.Context(), http.DefaultClient, target, nil)
	if err == nil {
		_ = resp.Body.Close()

		t.Fatal("doGet() error = nil, want connection error")
	}

	if strings.Contains(err.Error(), "SUPERSECRETKEY") {
		t.Errorf("error leaks API key: %v", err)
	}
}

func TestDoGetPreservesCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	resp, err := doGet(ctx, http.DefaultClient, "http://127.0.0.1:1/", nil)
	if err == nil {
		_ = resp.Body.Close()

		t.Fatal("doGet() error = nil, want cancellation error")
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want wrapping context.Canceled", err)
	}
}
