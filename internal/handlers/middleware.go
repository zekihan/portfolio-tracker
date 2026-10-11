package handlers

import (
	"mime"
	"net/http"

	"github.com/gin-gonic/gin"
)

// maxRequestBodyBytes caps the size of any request body accepted by the API.
const maxRequestBodyBytes = 1 << 20

// requireJSONBody bounds request bodies and rejects bodies that are not JSON.
// Handlers decode JSON regardless of Content-Type, so without this check a
// cross-site "text/plain" or form POST could reach mutating endpoints.
func requireJSONBody() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if ctx.Request.Body == nil || ctx.Request.Body == http.NoBody {
			ctx.Next()

			return
		}

		mediaType, _, err := mime.ParseMediaType(ctx.GetHeader("Content-Type"))
		if err != nil || mediaType != "application/json" {
			ctx.AbortWithStatusJSON(http.StatusUnsupportedMediaType, gin.H{
				errorKey: "request body must be application/json",
			})

			return
		}

		if ctx.Request.ContentLength > maxRequestBodyBytes {
			ctx.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
				errorKey: "request body too large",
			})

			return
		}

		ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maxRequestBodyBytes)
		ctx.Next()
	}
}

func protectBrowserRequests() gin.HandlerFunc {
	protection := http.NewCrossOriginProtection()

	return func(ctx *gin.Context) {
		ctx.Header("X-Content-Type-Options", "nosniff")
		ctx.Header("X-Frame-Options", "SAMEORIGIN")

		err := protection.Check(ctx.Request)
		if err != nil {
			ctx.AbortWithStatusJSON(
				http.StatusForbidden,
				gin.H{errorKey: "cross-origin request rejected"},
			)

			return
		}

		ctx.Next()
	}
}
