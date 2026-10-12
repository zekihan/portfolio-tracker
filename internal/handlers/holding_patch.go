package handlers

import (
	"net/http"
	"strconv"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

func (handler *Handler) patchHolding(writer http.ResponseWriter, request *http.Request,
) {
	id, err := strconv.Atoi(request.PathValue("id"))
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: invalidIDMsg})

		return
	}

	var patch models.HoldingPatch

	err = decodeJSON(request, &patch)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: err.Error()})

		return
	}

	if patch.Name == nil && patch.Ticker == nil &&
		patch.Basket == nil && patch.Location == nil && patch.Notes == nil &&
		patch.Quantity == nil &&
		patch.EstPrice == nil {
		writeJSON(
			writer,
			http.StatusBadRequest,
			map[string]any{errorKey: "at least one editable field is required"},
		)

		return
	}

	updated, err := handler.holdingSvc.Patch(request.Context(), id, patch)
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	if !updated {
		writeJSON(writer, http.StatusNotFound, map[string]any{errorKey: "holding not found"})

		return
	}

	writer.WriteHeader(http.StatusNoContent)
}
