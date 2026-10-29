package handlers

import (
	"errors"
	"net/http"

	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

// A missing key preserves backwards compatibility; an explicitly empty or
// repeated header is invalid rather than silently disabling deduplication.
func holdingCreateKey(request *http.Request) (string, bool) {
	keys := request.Header.Values("Idempotency-Key")
	if len(keys) == 0 {
		return "", true
	}

	if len(keys) != 1 || keys[0] == "" {
		return "", false
	}

	return keys[0], true
}

func (handler *Handler) createHolding(writer http.ResponseWriter, request *http.Request,
) {
	requestCtx := request.Context()

	key, valid := holdingCreateKey(request)
	if !valid {
		writeJSON(
			writer,
			http.StatusBadRequest,
			map[string]any{errorKey: services.ErrInvalidIdempotencyKey.Error()},
		)

		return
	}

	var holding models.Holding

	err := decodeJSON(request, &holding)
	if err != nil {
		respondJSONDecodeError(writer, err)

		return
	}

	if holding.ManualPrice.Valid && holding.ManualPrice.Float64 < 0 {
		writeJSON(
			writer,
			http.StatusBadRequest,
			map[string]any{errorKey: "manual price must be nonnegative"},
		)

		return
	}

	err = handler.holdingSvc.CreateWithIdempotencyKey(requestCtx, &holding, key)
	if err != nil {
		if errors.Is(err, services.ErrHoldingIdempotencyConflict) {
			writeJSON(writer, http.StatusUnprocessableEntity, map[string]any{errorKey: err.Error()})

			return
		}

		if errors.Is(err, services.ErrInvalidIdempotencyKey) {
			writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: err.Error()})

			return
		}

		respondHoldingMutationError(writer, request, err)

		return
	}

	writeJSON(writer, http.StatusCreated, holding)
}
