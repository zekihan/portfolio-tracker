package handlers

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/zekihan/portfolio-tracker/internal/price"
)

func respondHoldingMutationError(writer http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeJSON(writer, http.StatusNotFound, map[string]any{errorKey: "holding not found"})
	case errors.Is(err, price.ErrUnknownProvider):
		writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: "unknown price provider"})
	default:
		respondInternalError(writer, request, err)
	}
}

func apiDisplayCurrency(writer http.ResponseWriter, request *http.Request) (string, bool) {
	currency := currencyTRY
	if request.URL.Query().Has("currency") {
		currency = request.URL.Query().Get("currency")
	}

	if currency != "TRY" && currency != "USD" {
		writeJSON(
			writer,
			http.StatusBadRequest,
			map[string]any{errorKey: "unsupported display currency: use TRY or USD"},
		)

		return "", false
	}

	return currency, true
}
