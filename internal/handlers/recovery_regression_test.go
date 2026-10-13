package handlers

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecoverRequestsBeforeHeaders(t *testing.T) {
	t.Parallel()

	response := httptest.NewRecorder()
	handler := recoverRequests(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("unexpected") }),
	)
	handler.ServeHTTP(
		response,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody),
	)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}

	if response.Body.Len() != 0 {
		t.Fatal("panic details leaked")
	}
}

func TestRecoverRequestsAbortsPartialResponses(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"abort", "headers", "body", "flush"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			response := httptest.NewRecorder()
			handler := recoverRequests(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch mode {
					case "abort":
						panic(http.ErrAbortHandler)
					case "headers":
						w.WriteHeader(http.StatusAccepted)
					case "body":
						_, _ = w.Write([]byte("partial"))
					case "flush":
						err := http.NewResponseController(w).Flush()
						if err != nil {
							t.Fatal(err)
						}
					}

					panic("unexpected")
				}),
			)

			defer func() {
				got := recover()
				err, ok := got.(error)

				if !ok || !errors.Is(err, http.ErrAbortHandler) {
					t.Errorf("panic = %v, want ErrAbortHandler", got)
				}
			}()

			handler.ServeHTTP(
				response,
				httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody),
			)
		})
	}
}

func TestRecoverRequestsClosesPartialHTTPResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(
		recoverRequests(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, writeErr := writer.Write([]byte("partial"))
			if writeErr != nil {
				return
			}

			flushErr := http.NewResponseController(writer).Flush()
			if flushErr != nil {
				return
			}

			panic("interrupted response")
		})),
	)

	t.Cleanup(server.Close)

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}

	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("read error = %v, want incomplete response", err)
	}

	if string(body) != "partial" {
		t.Fatalf("body = %q", body)
	}
}
