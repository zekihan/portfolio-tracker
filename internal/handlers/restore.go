package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/zekihan/portfolio-tracker/internal/services"
)

// maxRestoreBytes caps the size of an uploaded backup file.
const maxRestoreBytes = 256 << 20

const sqliteHeader = "SQLite format 3\x00"

// requireRestoreConfirmation rejects restore requests that lack the custom
// header. Browsers cannot set it on cross-site requests without a CORS
// preflight, which this server does not grant, so cross-site form posts cannot
// trigger a restore.
func requireRestoreConfirmation() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Header.Get("X-Restore-Confirm") != "restore" {
				writeJSON(writer, http.StatusForbidden, map[string]any{
					errorKey: "restore must be confirmed from the settings page",
				})

				return
			}

			next.ServeHTTP(writer, request)
		})
	}
}

// restoreBackup replaces all holdings, snapshots, prices, and settings with the
// uploaded SQLite backup.
func (handler *Handler) restoreBackup(writer http.ResponseWriter, request *http.Request,
) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRestoreBytes)

	upload, _, err := request.FormFile("file")
	if err != nil {
		if tooLarge, isTooLarge := errors.AsType[*http.MaxBytesError](err); isTooLarge {
			writeJSON(
				writer,
				http.StatusRequestEntityTooLarge,
				map[string]any{
					errorKey: fmt.Sprintf("backup file exceeds %d bytes", tooLarge.Limit),
				},
			)

			return
		}

		writeJSON(
			writer,
			http.StatusBadRequest,
			map[string]any{errorKey: "backup file is required"},
		)

		return
	}

	defer func(requestCtx context.Context) {
		_ = upload.Close()

		if request.MultipartForm != nil {
			cleanupErr := request.MultipartForm.RemoveAll()
			if cleanupErr != nil {
				slog.ErrorContext(
					requestCtx,
					"Upload cleanup failed",
					slog.Any("error", cleanupErr),
				)
			}
		}
	}(request.Context())

	dir, err := os.MkdirTemp("", "portfolio-restore-")
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	defer func() {
		_ = os.RemoveAll(dir)
	}()

	uploadPath := filepath.Join(dir, "upload.db")

	destination, err := os.Create(uploadPath)
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	_, copyErr := io.Copy(destination, upload)
	closeErr := destination.Close()

	err = errors.Join(copyErr, closeErr)
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	isSQLite, err := hasSQLiteHeader(uploadPath)
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	if !isSQLite {
		writeJSON(
			writer,
			http.StatusBadRequest,
			map[string]any{errorKey: "file is not a SQLite database"},
		)

		return
	}

	err = handler.settingsSvc.RestoreFrom(request.Context(), uploadPath)
	if errors.Is(err, services.ErrIncompatibleBackup) {
		writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: err.Error()})

		return
	}

	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	writeJSON(writer, http.StatusOK, map[string]any{"success": true})
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
