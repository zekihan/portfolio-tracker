package handlers

import (
	"log/slog"
	"net/http"
)

// internalErrorMsg is the only detail returned to clients for server-side failures.
const internalErrorMsg = "internal server error"

// respondInternalError logs the underlying error and returns a generic 500
// response, so database and provider internals are not exposed to clients.
func respondInternalError(writer http.ResponseWriter, request *http.Request,
	err error,
) {
	slog.ErrorContext(request.Context(), "Request failed",
		slog.String("method", request.Method),
		slog.String("path", request.URL.Path),
		slog.Any("error", err),
	)
	writeJSON(writer, http.StatusInternalServerError, map[string]any{errorKey: internalErrorMsg})
}
