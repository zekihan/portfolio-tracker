package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"time"
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
		defer func(requestCtx context.Context) {
			if recovered := recover(); recovered != nil {
				slog.ErrorContext(
					requestCtx,
					"Request panicked",
					slog.Any("panic", recovered),
				)
				writer.WriteHeader(http.StatusInternalServerError)
			}
		}(request.Context())

		next.ServeHTTP(writer, request)
	})
}

type responseStatus struct {
	http.ResponseWriter

	status int
}

func (response *responseStatus) WriteHeader(status int) {
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
