package handlers

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"github.com/zekihan/portfolio-tracker/internal/services"
)

// maxRestoreBytes caps the size of an uploaded backup file.
const maxRestoreBytes = 256 << 20

const sqliteHeader = "SQLite format 3\x00"

// requireRestoreConfirmation rejects restore requests that lack the custom
// header. Browsers cannot set it on cross-site requests without a CORS
// preflight, which this server does not grant, so cross-site form posts cannot
// trigger a restore.
func requireRestoreConfirmation() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if ctx.GetHeader("X-Restore-Confirm") != "restore" {
			ctx.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				errorKey: "restore must be confirmed from the settings page",
			})

			return
		}

		ctx.Next()
	}
}

// restoreBackup replaces all holdings, snapshots, prices, and settings with the
// uploaded SQLite backup.
func (handler *Handler) restoreBackup(ctx *gin.Context) {
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maxRestoreBytes)

	fileHeader, err := ctx.FormFile("file")
	if err != nil {
		if tooLarge, isTooLarge := errors.AsType[*http.MaxBytesError](err); isTooLarge {
			ctx.AbortWithStatusJSON(
				http.StatusRequestEntityTooLarge,
				gin.H{errorKey: fmt.Sprintf("backup file exceeds %d bytes", tooLarge.Limit)},
			)

			return
		}

		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{errorKey: "backup file is required"})

		return
	}

	dir, err := os.MkdirTemp("", "portfolio-restore-")
	if err != nil {
		respondInternalError(ctx, err)

		return
	}

	defer func() {
		_ = os.RemoveAll(dir)
	}()

	uploadPath := filepath.Join(dir, "upload.db")

	err = ctx.SaveUploadedFile(fileHeader, uploadPath)
	if err != nil {
		respondInternalError(ctx, err)

		return
	}

	isSQLite, err := hasSQLiteHeader(uploadPath)
	if err != nil {
		respondInternalError(ctx, err)

		return
	}

	if !isSQLite {
		ctx.AbortWithStatusJSON(
			http.StatusBadRequest,
			gin.H{errorKey: "file is not a SQLite database"},
		)

		return
	}

	err = handler.settingsSvc.RestoreFrom(ctx.Request.Context(), uploadPath)
	if errors.Is(err, services.ErrIncompatibleBackup) {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{errorKey: err.Error()})

		return
	}

	if err != nil {
		respondInternalError(ctx, err)

		return
	}

	ctx.JSON(http.StatusOK, gin.H{"success": true})
}

func hasSQLiteHeader(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("open upload: %w", err)
	}

	defer func() {
		_ = file.Close()
	}()

	header := make([]byte, len(sqliteHeader))

	_, err = io.ReadFull(file, header)
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("read upload header: %w", err)
	}

	return string(header) == sqliteHeader, nil
}
