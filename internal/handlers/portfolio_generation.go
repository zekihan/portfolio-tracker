package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

const portfolioGenerationHeader = "X-Portfolio-Generation"

func requirePortfolioGeneration(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		values := request.Header.Values(portfolioGenerationHeader)
		if len(values) != 1 || values[0] == "" {
			writeJSON(
				writer,
				http.StatusPreconditionRequired,
				map[string]any{
					errorKey: "X-Portfolio-Generation is required; read current data before writing",
				},
			)

			return
		}

		generation, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || generation < 0 || strconv.FormatInt(generation, 10) != values[0] {
			writeJSON(
				writer,
				http.StatusBadRequest,
				map[string]any{errorKey: "invalid X-Portfolio-Generation"},
			)

			return
		}

		next.ServeHTTP(
			writer,
			request.WithContext(db.WithPortfolioGeneration(request.Context(), generation)),
		)
	})
}

func (handler *Handler) generationReader() func(context.Context) (int64, error) {
	switch {
	case handler.holdingSvc != nil:
		return handler.holdingSvc.PortfolioGeneration
	case handler.settingsSvc != nil:
		return handler.settingsSvc.PortfolioGeneration
	case handler.snapshotSvc != nil:
		return handler.snapshotSvc.PortfolioGeneration
	case handler.priceSvc != nil:
		return handler.priceSvc.PortfolioGeneration
	default:
		return nil
	}
}

// Capture before any handler reads: a racing replacement can produce a harmless
// stale token, but must never attach a newer token to previously read data.
func (handler *Handler) capturePortfolioGeneration(next http.Handler) http.Handler {
	read := handler.generationReader()

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || strings.HasPrefix(request.URL.Path, "/static") ||
			read == nil {
			next.ServeHTTP(writer, request)

			return
		}

		generation, err := read(request.Context())
		if err != nil {
			// Let existing unavailable-page handlers render a retry view, without
			// issuing a mutation token when the generation could not be read.
			next.ServeHTTP(writer, request)

			return
		}

		writer.Header().Set(portfolioGenerationHeader, strconv.FormatInt(generation, 10))
		next.ServeHTTP(
			writer,
			request.WithContext(db.WithPortfolioGeneration(request.Context(), generation)),
		)
	})
}
