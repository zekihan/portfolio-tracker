package price

import (
	"net/http"
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
