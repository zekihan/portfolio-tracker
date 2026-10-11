package handlers

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// internalErrorMsg is the only detail returned to clients for server-side failures.
const internalErrorMsg = "internal server error"

// respondInternalError logs the underlying error and returns a generic 500
// response, so database and provider internals are not exposed to clients.
func respondInternalError(ctx *gin.Context, err error) {
	slog.Error("Request failed",
		slog.String("method", ctx.Request.Method),
		slog.String("path", ctx.Request.URL.Path),
		slog.Any("error", err),
	)
	ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: internalErrorMsg})
}
