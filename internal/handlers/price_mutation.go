package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
)

func updateHoldingPrice(writer http.ResponseWriter, request *http.Request,
	field string, update func(context.Context, int, float64) error,
) {
	id, err := strconv.Atoi(request.PathValue("id"))
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: invalidIDMsg})

		return
	}

	var fields map[string]json.RawMessage

	err = decodeJSON(request, &fields)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: err.Error()})

		return
	}

	var value *float64

	err = json.Unmarshal(fields[field], &value)
	if err != nil || value == nil || *value < 0 {
		writeJSON(writer, http.StatusBadRequest,
			map[string]any{errorKey: field + " must be an explicitly supplied nonnegative number"})

		return
	}

	err = update(request.Context(), id, *value)
	if err != nil {
		respondHoldingMutationError(writer, request, err)

		return
	}

	writer.WriteHeader(http.StatusOK)
}
