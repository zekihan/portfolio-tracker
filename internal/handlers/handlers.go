package handlers

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

// Template helper functions.
func formatNumber(n float64) string {
	if n == 0 {
		return "0.00"
	}

	neg := n < 0
	if neg {
		n = -n
	}

	parts := strings.Split(fmt.Sprintf("%.2f", n), ".")
	intPart := parts[0]
	fracPart := parts[1]

	result := ""

	var resultSb33 strings.Builder

	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			resultSb33.WriteString(",")
		}

		resultSb33.WriteRune(r)
	}

	result += resultSb33.String()

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
	return t.Format("2006-01-02 15:04 UTC")
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)

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

func (h *Handler) SetupRoutes() *gin.Engine {
	r := gin.Default()
	r.SetTrustedProxies(nil)

	// Add template functions.
	r.SetFuncMap(template.FuncMap{
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
	})

	// Static files.
	r.Static("/static", "./web/static")
	r.LoadHTMLGlob("web/templates/*.html")

	// API routes.
	api := r.Group("/api")
	{
		// Holdings.
		api.GET("/holdings", h.getHoldings)
		api.GET("/holdings/:id", h.getHolding)
		api.POST("/holdings", h.createHolding)
		api.PUT("/holdings/:id", h.updateHolding)
		api.PATCH("/holdings/:id/est-price", h.updateEstPrice)
		api.PATCH("/holdings/:id/manual-price", h.updateManualPrice)
		api.DELETE("/holdings/:id/manual-price", h.clearManualPrice)
		api.DELETE("/holdings/:id", h.deleteHolding)

		// Dashboard.
		api.GET("/dashboard", h.getDashboard)
		api.GET("/dashboard/allocation", h.getAllocation)

		// History.
		api.GET("/history/snapshots", h.getSnapshots)
		api.GET("/history/chart", h.getChartData)
		api.GET("/history/holding/:id", h.getHoldingHistory)

		// Price Sources.
		api.GET("/sources/providers", h.getProviders)
		api.GET("/sources/mapping", h.getProviderMapping)
		api.POST("/sources/test", h.testProvider)
		api.POST("/sources/refresh", h.refreshPrices)
		api.PUT("/holdings/:id/provider", h.updateHoldingProvider)

		// Settings.
		api.GET("/settings", h.getSettings)
		api.PATCH("/settings/currency", h.updateCurrency)
		api.PATCH("/settings/dark-mode", h.updateDarkMode)
		api.PATCH("/settings/refresh-interval", h.updateRefreshInterval)
	}

	// Page routes (server-rendered).
	r.GET("/", h.pageDashboard)
	r.GET("/holdings", h.pageHoldings)
	r.GET("/history", h.pageHistory)
	r.GET("/sources", h.pageSources)
	r.GET("/settings", h.pageSettings)

	return r
}

// Holdings API.
func (h *Handler) getHoldings(c *gin.Context) {
	ctx := c.Request.Context()
	displayCurrency := c.DefaultQuery("currency", "TRY")

	holdings, err := h.holdingSvc.GetWithPrices(ctx, displayCurrency)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

		return
	}

	c.JSON(http.StatusOK, holdings)
}

func (h *Handler) getHolding(c *gin.Context) {
	ctx := c.Request.Context()
	id, _ := strconv.Atoi(c.Param("id"))

	holding, err := h.holdingSvc.GetByID(ctx, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

		return
	}

	if holding == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "holding not found"})

		return
	}

	c.JSON(http.StatusOK, holding)
}

func (h *Handler) createHolding(c *gin.Context) {
	ctx := c.Request.Context()

	var holding models.Holding

	err := c.ShouldBindJSON(&holding)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})

		return
	}

	err = h.holdingSvc.Create(ctx, &holding)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

		return
	}

	c.JSON(http.StatusCreated, holding)
}

func (h *Handler) updateHolding(c *gin.Context) {
	ctx := c.Request.Context()
	id, _ := strconv.Atoi(c.Param("id"))

	var holding models.Holding

	err := c.ShouldBindJSON(&holding)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})

		return
	}

	holding.ID = id

	err = h.holdingSvc.Update(ctx, &holding)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

		return
	}

	c.JSON(http.StatusOK, holding)
}

func (h *Handler) updateEstPrice(c *gin.Context) {
	ctx := c.Request.Context()
	id, _ := strconv.Atoi(c.Param("id"))

	var req struct {
		EstPrice float64 `json:"est_price"`
	}

	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})

		return
	}

	err = h.holdingSvc.UpdateEstPrice(ctx, id, req.EstPrice)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

		return
	}

	c.Status(http.StatusOK)
}

func (h *Handler) updateManualPrice(c *gin.Context) {
	ctx := c.Request.Context()
	id, _ := strconv.Atoi(c.Param("id"))

	var req struct {
		Price float64 `json:"price"`
	}

	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})

		return
	}

	err = h.holdingSvc.UpdateManualPrice(ctx, id, req.Price)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

		return
	}

	c.Status(http.StatusOK)
}

func (h *Handler) clearManualPrice(c *gin.Context) {
	ctx := c.Request.Context()
	id, _ := strconv.Atoi(c.Param("id"))

	err := h.holdingSvc.ClearManualPrice(ctx, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

		return
	}

	c.Status(http.StatusOK)
}

func (h *Handler) deleteHolding(c *gin.Context) {
	ctx := c.Request.Context()
	id, _ := strconv.Atoi(c.Param("id"))

	err := h.holdingSvc.Delete(ctx, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

		return
	}

	c.Status(http.StatusOK)
}

// Dashboard API.
func (h *Handler) getDashboard(c *gin.Context) {
	ctx := c.Request.Context()
	displayCurrency := c.DefaultQuery("currency", "TRY")

	data, err := h.holdingSvc.GetDashboardData(ctx, displayCurrency)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

		return
	}

	c.JSON(http.StatusOK, data)
}

func (h *Handler) getAllocation(c *gin.Context) {
	ctx := c.Request.Context()
	displayCurrency := c.DefaultQuery("currency", "TRY")

	data, err := h.holdingSvc.GetDashboardData(ctx, displayCurrency)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

		return
	}

	c.JSON(http.StatusOK, gin.H{
		"by_type":   data.AllocationByType,
		"by_basket": data.AllocationByBasket,
		"by_loc":    data.AllocationByLoc,
		"by_curr":   data.AllocationByCurr,
	})
}

// History API.
func (h *Handler) getSnapshots(c *gin.Context) {
	ctx := c.Request.Context()
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	snapshots, err := h.snapshotSvc.GetSnapshots(ctx, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

		return
	}

	c.JSON(http.StatusOK, snapshots)
}

func (h *Handler) getChartData(c *gin.Context) {
	ctx := c.Request.Context()
	days, _ := strconv.Atoi(c.DefaultQuery("days", "365"))

	points, err := h.snapshotSvc.GetSnapshotsForChart(ctx, days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

		return
	}

	c.JSON(http.StatusOK, points)
}

func (h *Handler) getHoldingHistory(c *gin.Context) {
	ctx := c.Request.Context()
	id, _ := strconv.Atoi(c.Param("id"))
	days, _ := strconv.Atoi(c.DefaultQuery("days", "365"))

	points, err := h.snapshotSvc.GetHoldingHistory(ctx, id, days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

		return
	}

	c.JSON(http.StatusOK, points)
}

// Price Sources API.
func (h *Handler) getProviders(c *gin.Context) {
	health, err := h.priceSvc.GetProviderHealth()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

		return
	}

	c.JSON(http.StatusOK, health)
}

func (h *Handler) getProviderMapping(c *gin.Context) {
	mapping, err := h.priceSvc.GetHoldingProviderMapping()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

		return
	}

	c.JSON(http.StatusOK, mapping)
}

func (h *Handler) testProvider(c *gin.Context) {
	ctx := c.Request.Context()

	var req struct {
		Provider     string `json:"provider"`
		InstrumentID string `json:"instrument_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})

		return
	}

	priceData, err := h.priceSvc.TestProvider(ctx, req.Provider, req.InstrumentID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"error":   err.Error(),
		})

		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"price":      priceData.Value,
		"currency":   priceData.Currency,
		"source":     priceData.Source,
		"fetched_at": priceData.FetchedAt.Format(time.RFC3339),
	})
}

func (h *Handler) refreshPrices(c *gin.Context) {
	ctx := c.Request.Context()

	holdings, err := h.holdingSvc.GetAll(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

		return
	}

	if err := h.priceSvc.RefreshAll(ctx, holdings); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

		return
	}

	c.Status(http.StatusOK)
}

func (h *Handler) updateHoldingProvider(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))

	var req struct {
		Provider     string `json:"provider"`
		InstrumentID string `json:"instrument_id"`
	}

	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})

		return
	}

	err = h.priceSvc.UpdateHoldingProvider(id, req.Provider, req.InstrumentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

		return
	}

	c.Status(http.StatusOK)
}

// Settings API.
func (h *Handler) getSettings(c *gin.Context) {
	settings, err := h.settingsSvc.GetAll(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

		return
	}

	c.JSON(http.StatusOK, settings)
}

func (h *Handler) updateCurrency(c *gin.Context) {
	ctx := c.Request.Context()

	var req struct {
		Currency string `json:"currency"`
	}

	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})

		return
	}

	if req.Currency != "TRY" && req.Currency != "USD" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid currency"})

		return
	}

	err = h.settingsSvc.SetDisplayCurrency(ctx, req.Currency)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

		return
	}

	c.Status(http.StatusOK)
}

func (h *Handler) updateDarkMode(c *gin.Context) {
	ctx := c.Request.Context()

	var req struct {
		Mode string `json:"mode"`
	}

	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})

		return
	}

	if req.Mode != "system" && req.Mode != "light" && req.Mode != "dark" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid mode"})

		return
	}

	err = h.settingsSvc.SetDarkMode(ctx, req.Mode)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

		return
	}

	c.Status(http.StatusOK)
}

func (h *Handler) updateRefreshInterval(c *gin.Context) {
	ctx := c.Request.Context()

	var req struct {
		Interval int `json:"interval"`
	}

	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})

		return
	}

	if req.Interval < 10 || req.Interval > 3600 {
		c.JSON(
			http.StatusBadRequest,
			gin.H{"error": "interval must be between 10 and 3600 seconds"},
		)

		return
	}

	err = h.settingsSvc.SetRefreshInterval(ctx, req.Interval)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

		return
	}

	c.Status(http.StatusOK)
}

// Page handlers (server-rendered with HTMX).
func (h *Handler) pageDashboard(c *gin.Context) {
	ctx := c.Request.Context()
	displayCurrency := c.DefaultQuery("currency", "TRY")

	data, err := h.holdingSvc.GetDashboardData(ctx, displayCurrency)
	if err != nil {
		data = &models.DashboardData{}
	}

	c.HTML(http.StatusOK, "dashboard.html", gin.H{
		"Data":            data,
		"DisplayCurrency": displayCurrency,
		"Title":           "Portfolio Tracker",
	})
}

func (h *Handler) pageHoldings(c *gin.Context) {
	ctx := c.Request.Context()
	displayCurrency := c.DefaultQuery("currency", "TRY")

	holdings, err := h.holdingSvc.GetWithPrices(ctx, displayCurrency)
	if err != nil {
		holdings = []models.HoldingWithPrice{}
	}

	baskets, _ := h.holdingSvc.GetUniqueBaskets(ctx)
	locations, _ := h.holdingSvc.GetUniqueLocations(ctx)

	c.HTML(http.StatusOK, "holdings.html", gin.H{
		"Holdings":        holdings,
		"DisplayCurrency": displayCurrency,
		"Baskets":         baskets,
		"Locations":       locations,
		"Title":           "Holdings",
	})
}

func (h *Handler) pageHistory(c *gin.Context) {
	ctx := c.Request.Context()
	displayCurrency := c.DefaultQuery("currency", "TRY")

	days, _ := strconv.Atoi(c.DefaultQuery("days", "365"))
	chartData, _ := h.snapshotSvc.GetSnapshotsForChart(ctx, days)

	baskets, _ := h.holdingSvc.GetUniqueBaskets(ctx)
	locations, _ := h.holdingSvc.GetUniqueLocations(ctx)

	// Get unique asset types.
	var assetTypes []string

	_, _ = h.holdingSvc.GetAll(c.Request.Context()) // This will populate the db
	// For now, hardcode based on model.
	assetTypes = []string{"Stock", "ETF", "TR Fund", "Crypto", "Gold", "Cash"}

	c.HTML(http.StatusOK, "history.html", gin.H{
		"ChartData":       chartData,
		"DisplayCurrency": displayCurrency,
		"Days":            days,
		"Baskets":         baskets,
		"Locations":       locations,
		"AssetTypes":      assetTypes,
		"Title":           "History",
	})
}

func (h *Handler) pageSources(c *gin.Context) {
	providers, _ := h.priceSvc.GetProviderHealth()
	mapping, _ := h.priceSvc.GetHoldingProviderMapping()

	c.HTML(http.StatusOK, "sources.html", gin.H{
		"Providers": providers,
		"Mapping":   mapping,
		"Title":     "Price Sources",
	})
}

func (h *Handler) pageSettings(c *gin.Context) {
	settings, _ := h.settingsSvc.GetAll(c.Request.Context())

	c.HTML(http.StatusOK, "settings.html", gin.H{
		"Settings": settings,
		"Title":    "Settings",
	})
}

// Helper to render HTMX partials.
func (h *Handler) renderPartial(c *gin.Context, template string, data gin.H) {
	c.Header("HX-Trigger", "portfolio-updated")
	c.HTML(http.StatusOK, template, data)
}
