package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
)

func (handler *Handler) deleteSnapshot(writer http.ResponseWriter, request *http.Request) {
	id, err := strconv.Atoi(request.PathValue("id"))
	if err != nil || id <= 0 {
		writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: invalidIDMsg})

		return
	}

	err = handler.snapshotSvc.DeleteSnapshot(request.Context(), id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeJSON(writer, http.StatusNotFound, map[string]any{errorKey: "snapshot not found"})
	case err != nil:
		respondInternalError(writer, request, err)
	default:
		writer.WriteHeader(http.StatusNoContent)
	}
}

func (handler *Handler) clearHistory(writer http.ResponseWriter, request *http.Request) {
	confirmation := request.Header.Values("X-Clear-History-Confirm")
	if len(confirmation) != 1 || confirmation[0] != "clear-history" {
		writeJSON(
			writer,
			http.StatusBadRequest,
			map[string]any{
				errorKey: "clearing all history requires X-Clear-History-Confirm: clear-history",
			},
		)

		return
	}

	err := handler.snapshotSvc.ClearHistory(request.Context())
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	writer.WriteHeader(http.StatusNoContent)
}
