package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

func (handler *Handler) holdingForm(ctx *gin.Context) {
	var holding models.Holding

	holding.BaseCurrency = "TRY"
	holding.AssetType = models.AssetTypeStock

	if ctx.Param("id") != "" {
		id, err := strconv.Atoi(ctx.Param("id"))
		if err != nil || id <= 0 {
			ctx.AbortWithStatus(http.StatusBadRequest)

			return
		}

		existing, err := handler.holdingSvc.GetByID(ctx.Request.Context(), id)
		if err != nil {
			respondInternalError(ctx, err)

			return
		}

		if existing == nil {
			ctx.AbortWithStatus(http.StatusNotFound)

			return
		}

		holding = *existing
	}

	if ctx.FullPath() == "/partials/holding-duplicate/:id" {
		holding.ID = 0
		holding.Name += " (copy)"
	}

	providers, err := handler.priceSvc.GetProviderHealthContext(ctx.Request.Context())
	if err != nil {
		respondInternalError(ctx, err)

		return
	}

	name := "holding-form"
	if ctx.FullPath() == "/partials/manual-price/:id" {
		name = "manual-price"
	}

	ctx.HTML(http.StatusOK, name, gin.H{"Holding": holding, "Providers": providers})
}
