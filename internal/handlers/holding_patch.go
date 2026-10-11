package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

func (handler *Handler) patchHolding(ctx *gin.Context) {
	id, err := strconv.Atoi(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: invalidIDMsg})

		return
	}

	var patch models.HoldingPatch

	err = ctx.ShouldBindJSON(&patch)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: err.Error()})

		return
	}

	if patch.Name == nil && patch.Ticker == nil &&
		patch.Basket == nil && patch.Location == nil && patch.Notes == nil &&
		patch.Quantity == nil &&
		patch.EstPrice == nil {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: "at least one editable field is required"})

		return
	}

	updated, err := handler.holdingSvc.Patch(ctx.Request.Context(), id, patch)
	if err != nil {
		respondInternalError(ctx, err)

		return
	}

	if !updated {
		ctx.JSON(http.StatusNotFound, gin.H{errorKey: "holding not found"})

		return
	}

	ctx.Status(http.StatusNoContent)
}
