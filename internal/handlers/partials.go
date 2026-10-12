package handlers

import (
	"net/http"
	"strconv"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

func (handler *Handler) holdingForm(writer http.ResponseWriter, request *http.Request,
) {
	var holding models.Holding

	holding.BaseCurrency = currencyTRY
	holding.AssetType = models.AssetTypeStock

	if request.PathValue("id") != "" {
		id, err := strconv.Atoi(request.PathValue("id"))
		if err != nil || id <= 0 {
			writer.WriteHeader(http.StatusBadRequest)

			return
		}

		existing, err := handler.holdingSvc.GetByID(request.Context(), id)
		if err != nil {
			respondInternalError(writer, request, err)

			return
		}

		if existing == nil {
			writer.WriteHeader(http.StatusNotFound)

			return
		}

		holding = *existing
	}

	if request.Pattern == "GET /partials/holding-duplicate/{id}" {
		holding.ID = 0
		holding.Name += " (copy)"
	}

	providers, err := handler.priceSvc.GetProviderHealthContext(request.Context())
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	name := "holding-form"
	if request.Pattern == "GET /partials/manual-price/{id}" {
		name = "manual-price"
	}

	handler.renderer.
		render(
			writer,
			http.StatusOK,
			name,
			map[string]any{"Holding": holding, "Providers": providers},
		)
}
