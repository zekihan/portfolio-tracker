package handlers

import (
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

const (
	errorKey       = "error"
	invalidIDMsg   = "invalid id"
	titleDashboard = "Portfolio Tracker"
	titleHoldings  = "Holdings"
	titleHistory   = "History"
	titleSources   = "Price Sources"
	titleSettings  = "Settings"
	percentScale   = 100
)

// Template helper functions.
func formatNumber(num float64) string {
	if math.IsNaN(num) || math.IsInf(num, 0) {
		return "N/A"
	}

	if num == 0 {
		return "0.00"
	}

	neg := num < 0
	if neg {
		num = -num
	}

	parts := strings.Split(fmt.Sprintf("%.2f", num), ".")
	intPart := parts[0]
	fracPart := parts[1]

	var resultSb strings.Builder

	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			resultSb.WriteString(",")
		}

		resultSb.WriteRune(r)
	}

	result := resultSb.String()

	if neg {
		result = "-" + result
	}

	return result + "." + fracPart
}

func gt(a, b float64) bool {
	return a > b
}

func lt(a, b float64) bool {
	return a < b
}

func formatCurrency(n float64, currency string) string {
	symbol := "₺"
	if currency == "USD" {
		symbol = "$"
	}

	return fmt.Sprintf("%s %s", symbol, formatNumber(n))
}

func formatPercent(n float64) string {
	return fmt.Sprintf("%.2f%%", n)
}

func formatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}

	return string(b)
}

type Handler struct {
	holdingSvc  *services.HoldingService
	snapshotSvc *services.SnapshotService
	settingsSvc *services.SettingsService
	priceSvc    *price.Service
}

func New(
	holdingSvc *services.HoldingService,
	snapshotSvc *services.SnapshotService,
	settingsSvc *services.SettingsService,
	priceSvc *price.Service,
) *Handler {
	return &Handler{
		holdingSvc:  holdingSvc,
		snapshotSvc: snapshotSvc,
		settingsSvc: settingsSvc,
		priceSvc:    priceSvc,
	}
}

func (handler *Handler) SetupRoutes() *gin.Engine {
	router := gin.Default()

	setTrustedProxiesErr := router.SetTrustedProxies(nil)
	if setTrustedProxiesErr != nil {
		return nil
	}

	// Add template functions.
	funcs := template.FuncMap{
		"coveragePercent": func(covered, total int) float64 {
			if total == 0 {
				return 0
			}

			return float64(covered) / float64(total) * percentScale
		},
		"formatNumber":   formatNumber,
		"formatCurrency": formatCurrency,
		"formatPercent":  formatPercent,
		"formatTime":     formatTime,
		"hasPrefix":      strings.HasPrefix,
		"contains":       strings.Contains,
		"toJSON":         toJSON,
		"gt":             gt,
		"lt":             lt,
		"sub":            func(a, b float64) float64 { return a - b },
		"add":            func(a, b int) int { return a + b },
		"mul":            func(a, b float64) float64 { return a * b },
		"div":            func(a, b float64) float64 { return a / b },
	}

	// Static files.
	router.Static("/static", "./web/static")
	// Each page owns its content block; sharing one template tree overwrites it.
	router.HTMLRender = newPageRenderer(funcs)

	// API routes.
	api := router.Group("/api")

	// Holdings.
	api.GET("/holdings", handler.getHoldings)
	api.GET("/holdings/:id", handler.getHolding)
	api.POST("/holdings", handler.createHolding)
	api.PUT("/holdings/:id", handler.updateHolding)
	api.PATCH("/holdings/:id", handler.patchHolding)
	api.PATCH("/holdings/:id/est-price", handler.updateEstPrice)
	api.PATCH("/holdings/:id/manual-price", handler.updateManualPrice)
	api.DELETE("/holdings/:id/manual-price", handler.clearManualPrice)
	api.DELETE("/holdings/:id", handler.deleteHolding)

	// Dashboard.
	api.GET("/dashboard", handler.getDashboard)
	api.GET("/dashboard/allocation", handler.getAllocation)

	// History.
	api.GET("/history/snapshots", handler.getSnapshots)
	api.GET("/history/chart", handler.getChartData)
	api.GET("/history/holding/:id", handler.getHoldingHistory)

	// Price Sources.
	api.GET("/sources/providers", handler.getProviders)
	api.GET("/sources/mapping", handler.getProviderMapping)
	api.POST("/sources/test", handler.testProvider)
	api.POST("/sources/refresh", handler.refreshPrices)
	api.PUT("/holdings/:id/provider", handler.updateHoldingProvider)

	// Settings.
	api.GET("/settings", handler.getSettings)
	api.PATCH("/settings/currency", handler.updateCurrency)
	api.PATCH("/settings/dark-mode", handler.updateDarkMode)
	api.PATCH("/settings/refresh-interval", handler.updateRefreshInterval)

	// Page routes (server-rendered).
	router.GET("/", handler.pageDashboard)
	router.GET("/holdings", handler.pageHoldings)
	router.GET("/history", handler.pageHistory)
	router.GET("/sources", handler.pageSources)
	router.GET("/settings", handler.pageSettings)

	return router
}

// Holdings API.
func (handler *Handler) getHoldings(ctx *gin.Context) {
	requestCtx := ctx.Request.Context()
	displayCurrency := ctx.DefaultQuery("currency", "TRY")

	holdings, err := handler.holdingSvc.GetWithPrices(requestCtx, displayCurrency)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: err.Error()})

		return
	}

	ctx.JSON(http.StatusOK, holdings)
}

func (handler *Handler) getHolding(ctx *gin.Context) {
	requestCtx := ctx.Request.Context()

	id, err := strconv.Atoi(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: invalidIDMsg})

		return
	}

	holding, err := handler.holdingSvc.GetByID(requestCtx, id)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: err.Error()})

		return
	}

	if holding == nil {
		ctx.JSON(http.StatusNotFound, gin.H{errorKey: "holding not found"})

		return
	}

	ctx.JSON(http.StatusOK, holding)
}

func (handler *Handler) createHolding(ctx *gin.Context) {
	requestCtx := ctx.Request.Context()

	var holding models.Holding

	err := ctx.ShouldBindJSON(&holding)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: err.Error()})

		return
	}

	err = handler.holdingSvc.Create(requestCtx, &holding)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: err.Error()})

		return
	}

	ctx.JSON(http.StatusCreated, holding)
}

func (handler *Handler) updateHolding(ctx *gin.Context) {
	requestCtx := ctx.Request.Context()

	id, err := strconv.Atoi(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: invalidIDMsg})

		return
	}

	var holding models.Holding

	err = ctx.ShouldBindJSON(&holding)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: err.Error()})

		return
	}

	holding.ID = id

	err = handler.holdingSvc.Update(requestCtx, &holding)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: err.Error()})

		return
	}

	ctx.JSON(http.StatusOK, holding)
}

func (handler *Handler) updateEstPrice(ctx *gin.Context) {
	requestCtx := ctx.Request.Context()

	id, err := strconv.Atoi(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: invalidIDMsg})

		return
	}

	var req struct {
		EstPrice float64 `json:"estPrice" binding:"gte=0"`
	}

	err = ctx.ShouldBindJSON(&req)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: err.Error()})

		return
	}

	err = handler.holdingSvc.UpdateEstPrice(requestCtx, id, req.EstPrice)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: err.Error()})

		return
	}

	ctx.Status(http.StatusOK)
}

func (handler *Handler) updateManualPrice(ctx *gin.Context) {
	requestCtx := ctx.Request.Context()

	id, err := strconv.Atoi(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: invalidIDMsg})

		return
	}

	var req struct {
		Price float64 `json:"price" binding:"gte=0"`
	}

	err = ctx.ShouldBindJSON(&req)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: err.Error()})

		return
	}

	err = handler.holdingSvc.UpdateManualPrice(requestCtx, id, req.Price)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: err.Error()})

		return
	}

	ctx.Status(http.StatusOK)
}

func (handler *Handler) clearManualPrice(ctx *gin.Context) {
	requestCtx := ctx.Request.Context()

	id, err := strconv.Atoi(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: invalidIDMsg})

		return
	}

	err = handler.holdingSvc.ClearManualPrice(requestCtx, id)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: err.Error()})

		return
	}

	ctx.Status(http.StatusOK)
}

func (handler *Handler) deleteHolding(ctx *gin.Context) {
	requestCtx := ctx.Request.Context()

	id, err := strconv.Atoi(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: invalidIDMsg})

		return
	}

	err = handler.holdingSvc.Delete(requestCtx, id)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: err.Error()})

		return
	}

	ctx.Status(http.StatusOK)
}

// Dashboard API.
func (handler *Handler) getDashboard(ctx *gin.Context) {
	requestCtx := ctx.Request.Context()
	displayCurrency := ctx.DefaultQuery("currency", "TRY")

	data, err := handler.holdingSvc.GetDashboardData(requestCtx, displayCurrency)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: err.Error()})

		return
	}

	ctx.JSON(http.StatusOK, data)
}

func (handler *Handler) getAllocation(ctx *gin.Context) {
	requestCtx := ctx.Request.Context()
	displayCurrency := ctx.DefaultQuery("currency", "TRY")

	data, err := handler.holdingSvc.GetDashboardData(requestCtx, displayCurrency)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: err.Error()})

		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"by_type":   data.AllocationByType,
		"by_basket": data.AllocationByBasket,
		"by_loc":    data.AllocationByLoc,
		"by_curr":   data.AllocationByCurr,
	})
}

// History API.
func (handler *Handler) getSnapshots(ctx *gin.Context) {
	requestCtx := ctx.Request.Context()

	limit, err := strconv.Atoi(ctx.DefaultQuery("limit", "50"))
	if err != nil || limit < 1 || limit > 1000 {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: "invalid limit"})

		return
	}

	offset, err := strconv.Atoi(ctx.DefaultQuery("offset", "0"))
	if err != nil || offset < 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: "invalid offset"})

		return
	}

	snapshots, err := handler.snapshotSvc.GetSnapshots(requestCtx, limit, offset)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: err.Error()})

		return
	}

	ctx.JSON(http.StatusOK, snapshots)
}

func (handler *Handler) getChartData(ctx *gin.Context) {
	requestCtx := ctx.Request.Context()

	days, err := strconv.Atoi(ctx.DefaultQuery("days", "365"))
	if err != nil || days < 1 || days > 36500 {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: "invalid days"})

		return
	}

	points, err := handler.snapshotSvc.GetSnapshotsForChart(requestCtx, days)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: err.Error()})

		return
	}

	ctx.JSON(http.StatusOK, points)
}

func (handler *Handler) getHoldingHistory(ctx *gin.Context) {
	requestCtx := ctx.Request.Context()

	id, err := strconv.Atoi(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: invalidIDMsg})

		return
	}

	days, err := strconv.Atoi(ctx.DefaultQuery("days", "365"))
	if err != nil || days < 1 || days > 36500 {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: "invalid days"})

		return
	}

	points, err := handler.snapshotSvc.GetHoldingHistory(requestCtx, id, days)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: err.Error()})

		return
	}

	ctx.JSON(http.StatusOK, points)
}

// Price Sources API.
func (handler *Handler) getProviders(ctx *gin.Context) {
	health, err := handler.priceSvc.GetProviderHealthContext(ctx.Request.Context())
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: err.Error()})

		return
	}

	ctx.JSON(http.StatusOK, health)
}

func (handler *Handler) getProviderMapping(ctx *gin.Context) {
	mapping, err := handler.priceSvc.GetHoldingProviderMappingContext(ctx.Request.Context())
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: err.Error()})

		return
	}

	ctx.JSON(http.StatusOK, mapping)
}

func (handler *Handler) testProvider(ctx *gin.Context) {
	requestCtx := ctx.Request.Context()

	var req struct {
		Provider     string `json:"provider"`
		InstrumentID string `json:"instrumentId"`
	}

	err := ctx.ShouldBindJSON(&req)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: err.Error()})

		return
	}

	priceData, err := handler.priceSvc.TestProvider(requestCtx, req.Provider, req.InstrumentID)
	if err != nil {
		ctx.JSON(http.StatusOK, gin.H{
			"success": false,
			"error":   err.Error(),
		})

		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"success":    true,
		"price":      priceData.Value,
		"currency":   priceData.Currency,
		"source":     priceData.Source,
		"fetched_at": priceData.FetchedAt.Format(time.RFC3339),
	})
}

func (handler *Handler) refreshPrices(ctx *gin.Context) {
	requestCtx := ctx.Request.Context()

	holdings, err := handler.holdingSvc.GetAll(requestCtx)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: err.Error()})

		return
	}

	err = handler.priceSvc.RefreshAll(requestCtx, holdings)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: err.Error()})

		return
	}

	ctx.Status(http.StatusOK)
}

func (handler *Handler) updateHoldingProvider(ctx *gin.Context) {
	id, err := strconv.Atoi(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: invalidIDMsg})

		return
	}

	var req struct {
		Provider     string `json:"provider"`
		InstrumentID string `json:"instrumentId"`
	}

	err = ctx.ShouldBindJSON(&req)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: err.Error()})

		return
	}

	err = handler.priceSvc.UpdateHoldingProviderContext(
		ctx.Request.Context(),
		id,
		req.Provider,
		req.InstrumentID,
	)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: err.Error()})

		return
	}

	ctx.Status(http.StatusOK)
}

// Settings API.
func (handler *Handler) getSettings(ctx *gin.Context) {
	settings, err := handler.settingsSvc.GetAll(ctx.Request.Context())
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: err.Error()})

		return
	}

	ctx.JSON(http.StatusOK, settings)
}

func (handler *Handler) updateCurrency(ctx *gin.Context) {
	requestCtx := ctx.Request.Context()

	var req struct {
		Currency string `json:"currency"`
	}

	err := ctx.ShouldBindJSON(&req)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: err.Error()})

		return
	}

	if req.Currency != "TRY" && req.Currency != "USD" {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: "invalid currency"})

		return
	}

	err = handler.settingsSvc.SetDisplayCurrency(requestCtx, req.Currency)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: err.Error()})

		return
	}

	ctx.Status(http.StatusOK)
}

func (handler *Handler) updateDarkMode(ctx *gin.Context) {
	requestCtx := ctx.Request.Context()

	var req struct {
		Mode string `json:"mode"`
	}

	err := ctx.ShouldBindJSON(&req)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: err.Error()})

		return
	}

	if req.Mode != "system" && req.Mode != "light" && req.Mode != "dark" {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: "invalid mode"})

		return
	}

	err = handler.settingsSvc.SetDarkMode(requestCtx, req.Mode)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: err.Error()})

		return
	}

	ctx.Status(http.StatusOK)
}

func (handler *Handler) updateRefreshInterval(ctx *gin.Context) {
	requestCtx := ctx.Request.Context()

	var req struct {
		Interval int `json:"interval"`
	}

	err := ctx.ShouldBindJSON(&req)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{errorKey: err.Error()})

		return
	}

	if req.Interval < 10 || req.Interval > 3600 {
		ctx.JSON(
			http.StatusBadRequest,
			gin.H{errorKey: "interval must be between 10 and 3600 seconds"},
		)

		return
	}

	err = handler.settingsSvc.SetRefreshInterval(requestCtx, req.Interval)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{errorKey: err.Error()})

		return
	}

	ctx.Status(http.StatusOK)
}

// Page handlers (server-rendered with HTMX).
func (handler *Handler) pageDashboard(ctx *gin.Context) {
	requestCtx := ctx.Request.Context()
	displayCurrency := ctx.DefaultQuery("currency", "TRY")

	data, err := handler.holdingSvc.GetDashboardData(requestCtx, displayCurrency)
	if err != nil {
		data = &models.DashboardData{} //nolint:exhaustruct_v5 // Empty struct for error fallback
	}

	ctx.HTML(http.StatusOK, "dashboard.html", gin.H{
		"Data":            data,
		"DisplayCurrency": displayCurrency,
		"Title":           titleDashboard, //nolint:goconst // Map key is required
	})
}

func (handler *Handler) pageHoldings(ctx *gin.Context) {
	requestCtx := ctx.Request.Context()
	displayCurrency := ctx.DefaultQuery("currency", "TRY")

	holdings, err := handler.holdingSvc.GetWithPrices(requestCtx, displayCurrency)
	if err != nil {
		holdings = []models.HoldingWithPrice{}
	}

	baskets, err := handler.holdingSvc.GetUniqueBaskets(requestCtx)
	if err != nil {
		baskets = []string{}
	}

	locations, err := handler.holdingSvc.GetUniqueLocations(requestCtx)
	if err != nil {
		locations = []string{}
	}

	ctx.HTML(http.StatusOK, "holdings.html", gin.H{
		"Holdings":        holdings,
		"DisplayCurrency": displayCurrency,
		"Baskets":         baskets,
		"Locations":       locations,
		"Title":           titleHoldings,
	})
}

func (handler *Handler) pageHistory(ctx *gin.Context) {
	requestCtx := ctx.Request.Context()
	displayCurrency := ctx.DefaultQuery("currency", "TRY")

	days, err := strconv.Atoi(ctx.DefaultQuery("days", "365"))
	if err != nil || days < 1 || days > 36500 {
		days = 365
	}

	chartData, err := handler.snapshotSvc.GetSnapshotsForChart(requestCtx, days)
	if err != nil {
		chartData = []services.ChartPoint{}
	}

	baskets, err := handler.holdingSvc.GetUniqueBaskets(requestCtx)
	if err != nil {
		baskets = []string{}
	}

	locations, err := handler.holdingSvc.GetUniqueLocations(requestCtx)
	if err != nil {
		locations = []string{}
	}

	// Get unique asset types.
	var assetTypes []string

	_, _ = handler.holdingSvc.GetAll(requestCtx) //nolint:errcheck // This will populate the db
	// For now, hardcode based on model.
	assetTypes = []string{"Stock", "ETF", "TR Fund", "Crypto", "Gold", "Cash"}

	ctx.HTML(http.StatusOK, "history.html", gin.H{
		"ChartData":       chartData,
		"DisplayCurrency": displayCurrency,
		"Days":            days,
		"Baskets":         baskets,
		"Locations":       locations,
		"AssetTypes":      assetTypes,
		"Title":           titleHistory,
	})
}

func (handler *Handler) pageSources(ctx *gin.Context) {
	providers, err := handler.priceSvc.GetProviderHealthContext(ctx.Request.Context())
	if err != nil {
		providers = []models.ProviderHealth{}
	}

	mapping, err := handler.priceSvc.GetHoldingProviderMappingContext(ctx.Request.Context())
	if err != nil {
		mapping = []price.HoldingProviderMapping{}
	}

	ctx.HTML(http.StatusOK, "sources.html", gin.H{
		"Providers": providers,
		"Mapping":   mapping,
		"Title":     titleSources,
	})
}

func (handler *Handler) pageSettings(ctx *gin.Context) {
	settings, err := handler.settingsSvc.GetAll(ctx.Request.Context())
	if err != nil {
		settings = models.Settings{} //nolint:exhaustruct_v5 // Empty struct for error fallback
	}

	ctx.HTML(http.StatusOK, "settings.html", gin.H{
		"Settings": settings,
		"Title":    titleSettings,
	})
}
