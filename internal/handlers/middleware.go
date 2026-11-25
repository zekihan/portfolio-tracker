package handlers

import (
	"errors"
	"mime"
	"net/http"

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

// normalizeAbort preserves net/http's abort behavior for wrapped abort errors.
func normalizeAbort(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				err, ok := recovered.(error)
				if ok && errors.Is(err, http.ErrAbortHandler) {
					panic(http.ErrAbortHandler)
				}

				panic(recovered)
			}
		}()

		next.ServeHTTP(w, r)
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
