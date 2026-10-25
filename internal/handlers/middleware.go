package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/price"
)

const maxRequestBodyBytes = 1 << 20

func requireJSONBody() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Body == nil || request.Body == http.NoBody {
				next.ServeHTTP(writer, request)

				return
			}

			mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
			if err != nil || mediaType != "application/json" {
				writeJSON(writer, http.StatusUnsupportedMediaType,
					map[string]any{errorKey: "request body must be application/json"})

				return
			}

			if request.ContentLength > maxRequestBodyBytes {
				writeJSON(writer, http.StatusRequestEntityTooLarge,
					map[string]any{errorKey: "request body too large"})

				return
			}

			request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBodyBytes)
			next.ServeHTTP(writer, request)
		})
	}
}

func protectBrowserRequests() func(http.Handler) http.Handler {
	protection := http.NewCrossOriginProtection()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("X-Content-Type-Options", "nosniff")
			writer.Header().Set("X-Frame-Options", "SAMEORIGIN")

			err := protection.Check(request)
			if err != nil {
				writeJSON(writer, http.StatusForbidden,
					map[string]any{errorKey: "cross-origin request rejected"})

				return
			}

			next.ServeHTTP(writer, request)
		})
	}
}

func recoverRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		response := &responseStatus{ResponseWriter: writer}

		defer func(requestCtx context.Context) {
			if recovered := recover(); recovered != nil {
				err, ok := recovered.(error)
				if ok && errors.Is(err, http.ErrAbortHandler) {
					panic(http.ErrAbortHandler)
				}

				slog.ErrorContext(
					requestCtx,
					"Request panicked",
					slog.Any("panic", recovered),
				)

				if response.status != 0 {
					// A partial response cannot be repaired; let net/http abort the stream.
					panic(http.ErrAbortHandler)
				}

				response.WriteHeader(http.StatusInternalServerError)
			}
		}(request.Context())

		next.ServeHTTP(response, request)
	})
}

type responseStatus struct {
	http.ResponseWriter

	status int
}

func (response *responseStatus) WriteHeader(status int) {
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		response.ResponseWriter.WriteHeader(status)

		return
	}

	if response.status == 0 {
		response.status = status
		response.ResponseWriter.WriteHeader(status)
	}
}

func (response *responseStatus) Write(body []byte) (int, error) {
	response.WriteHeader(http.StatusOK)

	written, err := response.ResponseWriter.Write(body)
	if err != nil {
		return written, fmt.Errorf("write response: %w", err)
	}

	return written, nil
}

func (response *responseStatus) FlushError() error {
	response.WriteHeader(http.StatusOK)

	err := http.NewResponseController(response.ResponseWriter).Flush()
	if err != nil {
		return fmt.Errorf("flush response: %w", err)
	}

	return nil
}

func (response *responseStatus) Unwrap() http.ResponseWriter {
	return response.ResponseWriter
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		response := &responseStatus{ResponseWriter: writer}
		next.ServeHTTP(response, request)

		status := response.status
		if status == 0 {
			status = http.StatusOK
		}

		slog.InfoContext(request.Context(), "HTTP request",
			slog.String("method", request.Method),
			slog.String("path", request.URL.Path),
			slog.Int("status", status),
			slog.Duration("duration", time.Since(started)))
	})
}

// A custom header cannot be sent by cross-site forms or subresource loads.
// The browser origin middleware must also accept the request before work starts.
func requireActionHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Portfolio-Action") != "1" {
			writeJSON(
				writer,
				http.StatusForbidden,
				map[string]any{errorKey: "X-Portfolio-Action: 1 is required"},
			)

			return
		}

		next.ServeHTTP(writer, request)
	})
}

func cachedPriceReads(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet || request.Method == http.MethodHead {
			request = request.WithContext(price.WithCachedQuotesOnly(request.Context()))
		}

		next.ServeHTTP(writer, request)
	})
}
