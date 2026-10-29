package handlers

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

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

func formatQuantity(num float64) string {
	if math.IsNaN(num) || math.IsInf(num, 0) {
		return "N/A"
	}

	return strconv.FormatFloat(num, 'f', -1, 64)
}

func gt(a, b float64) bool {
	return a > b
}

func lt(a, b float64) bool {
	return a < b
}

func formatCurrency(value float64, currency string) string {
	symbol := "₺"

	switch currency {
	case currencyUSD:
		symbol = "$"
	case currencyEUR:
		symbol = "€"
	}

	return fmt.Sprintf("%s %s", symbol, formatNumber(value))
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
	renderer    *pageRenderer
	backupMu    sync.Mutex
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

func (handler *Handler) SetupRoutes() http.Handler {
	router := http.NewServeMux()

	// Add template functions.
	funcs := template.FuncMap{
		"coveragePercent": func(covered, total int) float64 {
			if total == 0 {
				return 0
			}

			return float64(covered) / float64(total) * percentScale
		},
		"formatNumber":   formatNumber,
		"formatQuantity": formatQuantity,
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

	// Each page owns its content block; sharing one template tree overwrites it.
	handler.renderer = newPageRenderer(funcs)
	static := staticFiles("./web/static")
	router.Handle("GET /static/{path...}", static)
	router.Handle("HEAD /static/{path...}", static)
	router.Handle("GET /api/holdings", requireJSONBody()(http.HandlerFunc(handler.getHoldings)))
	router.Handle("GET /api/holdings/{id}", requireJSONBody()(http.HandlerFunc(handler.getHolding)))
	router.Handle(
		"POST /api/holdings",
		requireJSONBody()(requirePortfolioGeneration(http.HandlerFunc(handler.createHolding))),
	)
	router.Handle(
		"PUT /api/holdings/{id}",
		requireJSONBody()(requirePortfolioGeneration(http.HandlerFunc(handler.updateHolding))),
	)
	router.Handle(
		"PATCH /api/holdings/{id}",
		requireJSONBody()(requirePortfolioGeneration(http.HandlerFunc(handler.patchHolding))),
	)
	router.Handle(
		"PATCH /api/holdings/{id}/est-price",
		requireJSONBody()(requirePortfolioGeneration(http.HandlerFunc(handler.updateEstPrice))),
	)
	router.Handle(
		"PATCH /api/holdings/{id}/manual-price",
		requireJSONBody()(requirePortfolioGeneration(http.HandlerFunc(handler.updateManualPrice))),
	)
	router.Handle(
		"DELETE /api/holdings/{id}/manual-price",
		requireJSONBody()(requirePortfolioGeneration(http.HandlerFunc(handler.clearManualPrice))),
	)
	router.Handle(
		"DELETE /api/holdings/{id}",
		requireJSONBody()(requirePortfolioGeneration(http.HandlerFunc(handler.deleteHolding))),
	)
	router.Handle("GET /api/dashboard", requireJSONBody()(http.HandlerFunc(handler.getDashboard)))
	router.Handle(
		"GET /api/dashboard/allocation",
		requireJSONBody()(http.HandlerFunc(handler.getAllocation)),
	)
	router.Handle(
		"GET /api/history/snapshots",
		requireJSONBody()(http.HandlerFunc(handler.getSnapshots)),
	)
	router.Handle(
		"DELETE /api/history/snapshots/{id}",
		requireJSONBody()(requirePortfolioGeneration(http.HandlerFunc(handler.deleteSnapshot))),
	)
	router.Handle(
		"DELETE /api/history/snapshots",
		requireJSONBody()(requirePortfolioGeneration(http.HandlerFunc(handler.clearHistory))),
	)
	router.Handle(
		"GET /api/history/chart",
		requireJSONBody()(http.HandlerFunc(handler.getChartData)),
	)
	router.Handle(
		"GET /api/history/holding/{id}",
		requireJSONBody()(http.HandlerFunc(handler.getHoldingHistory)),
	)
	router.Handle(
		"GET /api/sources/providers",
		requireJSONBody()(http.HandlerFunc(handler.getProviders)),
	)
	router.Handle(
		"GET /api/sources/mapping",
		requireJSONBody()(http.HandlerFunc(handler.getProviderMapping)),
	)
	router.Handle(
		"POST /api/sources/test",
		requireActionHeader(requireJSONBody()(http.HandlerFunc(handler.testProvider))),
	)
	router.Handle(
		"POST /api/sources/refresh",
		requireActionHeader(
			requireJSONBody()(requirePortfolioGeneration(http.HandlerFunc(handler.refreshPrices))),
		),
	)
	router.Handle(
		"PUT /api/holdings/{id}/provider",
		requireJSONBody()(
			requirePortfolioGeneration(http.HandlerFunc(handler.updateHoldingProvider)),
		),
	)
	router.Handle("GET /api/settings", requireJSONBody()(http.HandlerFunc(handler.getSettings)))
	router.Handle(
		"PATCH /api/settings/currency",
		requireJSONBody()(requirePortfolioGeneration(http.HandlerFunc(handler.updateCurrency))),
	)
	router.Handle(
		"PATCH /api/settings/dark-mode",
		requireJSONBody()(requirePortfolioGeneration(http.HandlerFunc(handler.updateDarkMode))),
	)
	router.Handle(
		"PATCH /api/settings/refresh-interval",
		requireJSONBody()(
			requirePortfolioGeneration(http.HandlerFunc(handler.updateRefreshInterval)),
		),
	)
	router.Handle(
		"DELETE /api/settings/clear-all",
		requireJSONBody()(requirePortfolioGeneration(http.HandlerFunc(handler.clearAllData))),
	)
	router.Handle(
		"POST /api/settings/backup",
		requireActionHeader(requireJSONBody()(http.HandlerFunc(handler.downloadBackup))),
	)
	router.Handle(
		"POST /api/settings/restore",
		requireRestoreConfirmation()(
			requirePortfolioGeneration(http.HandlerFunc(handler.restoreBackup)),
		),
	)
	router.Handle("GET /partials/holding-form", http.HandlerFunc(handler.holdingForm))
	router.Handle("GET /partials/holding-form/{id}", http.HandlerFunc(handler.holdingForm))
	router.Handle("GET /partials/holding-duplicate/{id}", http.HandlerFunc(handler.holdingForm))
	router.Handle("GET /partials/manual-price/{id}", http.HandlerFunc(handler.holdingForm))
	router.HandleFunc("GET /{$}", handler.pageDashboard)
	router.HandleFunc("GET /holdings", handler.pageHoldings)
	router.HandleFunc("GET /history", handler.pageHistory)
	router.HandleFunc("GET /sources", handler.pageSources)
	router.HandleFunc("GET /settings", handler.pageSettings)
	router.HandleFunc("/", notFound)

	return logRequests(
		recoverRequests(
			handler.capturePortfolioGeneration(cachedPriceReads(compatibleRoutes(router, static))),
		),
	)
}

// Holdings API.
func (handler *Handler) getHoldings(writer http.ResponseWriter, request *http.Request,
) {
	requestCtx := request.Context()

	displayCurrency, valid := apiDisplayCurrency(writer, request)
	if !valid {
		return
	}

	holdings, err := handler.holdingSvc.GetWithPrices(requestCtx, displayCurrency)
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	writeJSON(writer, http.StatusOK, holdings)
}

func (handler *Handler) getHolding(writer http.ResponseWriter, request *http.Request,
) {
	requestCtx := request.Context()

	id, err := strconv.Atoi(request.PathValue("id"))
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: invalidIDMsg})

		return
	}

	holding, err := handler.holdingSvc.GetByID(requestCtx, id)
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	if holding == nil {
		writeJSON(writer, http.StatusNotFound, map[string]any{errorKey: "holding not found"})

		return
	}

	writeJSON(writer, http.StatusOK, holding)
}

func (handler *Handler) updateHolding(writer http.ResponseWriter, request *http.Request,
) {
	requestCtx := request.Context()

	id, err := strconv.Atoi(request.PathValue("id"))
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: invalidIDMsg})

		return
	}

	var holding models.Holding

	err = decodeJSON(request, &holding)
	if err != nil {
		respondJSONDecodeError(writer, err)

		return
	}

	holding.ID = id

	err = handler.holdingSvc.Update(requestCtx, &holding)
	if err != nil {
		respondHoldingMutationError(writer, request, err)

		return
	}

	writeJSON(writer, http.StatusOK, holding)
}

func (handler *Handler) updateEstPrice(writer http.ResponseWriter, request *http.Request) {
	updateHoldingPrice(writer, request, "estPrice", handler.holdingSvc.UpdateEstPrice)
}

func (handler *Handler) updateManualPrice(writer http.ResponseWriter, request *http.Request) {
	updateHoldingPrice(writer, request, "price", handler.holdingSvc.UpdateManualPrice)
}

func (handler *Handler) clearManualPrice(writer http.ResponseWriter, request *http.Request,
) {
	requestCtx := request.Context()

	id, err := strconv.Atoi(request.PathValue("id"))
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: invalidIDMsg})

		return
	}

	err = handler.holdingSvc.ClearManualPrice(requestCtx, id)
	if err != nil {
		respondHoldingMutationError(writer, request, err)

		return
	}

	writer.WriteHeader(http.StatusOK)
}

func (handler *Handler) deleteHolding(writer http.ResponseWriter, request *http.Request,
) {
	requestCtx := request.Context()

	id, err := strconv.Atoi(request.PathValue("id"))
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: invalidIDMsg})

		return
	}

	err = handler.holdingSvc.Delete(requestCtx, id)
	if err != nil {
		respondHoldingMutationError(writer, request, err)

		return
	}

	writer.WriteHeader(http.StatusOK)
}

// Dashboard API.
func (handler *Handler) getDashboard(writer http.ResponseWriter, request *http.Request,
) {
	requestCtx := request.Context()

	displayCurrency, valid := apiDisplayCurrency(writer, request)
	if !valid {
		return
	}

	data, err := handler.holdingSvc.GetDashboardData(requestCtx, displayCurrency)
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	writeJSON(writer, http.StatusOK, data)
}

func (handler *Handler) getAllocation(writer http.ResponseWriter, request *http.Request,
) {
	requestCtx := request.Context()

	displayCurrency, valid := apiDisplayCurrency(writer, request)
	if !valid {
		return
	}

	data, err := handler.holdingSvc.GetDashboardData(requestCtx, displayCurrency)
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	writeJSON(writer, http.StatusOK, map[string]any{
		"valuation_complete": data.ValuationComplete,
		"valued_count":       data.ValuedCount,
		"unvalued_count":     data.UnvaluedCount,
		"by_type":            data.AllocationByType,
		"by_basket":          data.AllocationByBasket,
		"by_loc":             data.AllocationByLoc,
		"by_curr":            data.AllocationByCurr,
	})
}

// History API.
func (handler *Handler) getSnapshots(writer http.ResponseWriter, request *http.Request,
) {
	requestCtx := request.Context()

	limit, err := strconv.Atoi(queryDefault(request, "limit", "50"))
	if err != nil || limit < 1 || limit > 1000 {
		writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: "invalid limit"})

		return
	}

	offset, err := strconv.Atoi(queryDefault(request, "offset", "0"))
	if err != nil || offset < 0 {
		writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: "invalid offset"})

		return
	}

	days, err := strconv.Atoi(queryDefault(request, "days", "0"))
	if err != nil || days < 0 || days > 36500 {
		writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: "invalid days"})

		return
	}

	snapshots, err := handler.snapshotSvc.GetSnapshots(requestCtx, limit, offset, days)
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	writeJSON(writer, http.StatusOK, snapshots)
}

func (handler *Handler) getChartData(writer http.ResponseWriter, request *http.Request,
) {
	requestCtx := request.Context()

	days, err := strconv.Atoi(queryDefault(request, "days", "365"))
	if err != nil || days < 1 || days > 36500 {
		writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: "invalid days"})

		return
	}

	points, err := handler.snapshotSvc.GetSnapshotsForChart(requestCtx, days)
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	writeJSON(writer, http.StatusOK, points)
}

func (handler *Handler) getHoldingHistory(writer http.ResponseWriter, request *http.Request,
) {
	requestCtx := request.Context()

	id, err := strconv.Atoi(request.PathValue("id"))
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: invalidIDMsg})

		return
	}

	days, err := strconv.Atoi(queryDefault(request, "days", "365"))
	if err != nil || days < 1 || days > 36500 {
		writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: "invalid days"})

		return
	}

	points, err := handler.snapshotSvc.GetHoldingHistory(requestCtx, id, days)
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	writeJSON(writer, http.StatusOK, points)
}

// downloadBackup sends a consistent SQLite copy of the database as a file download.
func (handler *Handler) downloadBackup(writer http.ResponseWriter, request *http.Request,
) {
	if !handler.backupMu.TryLock() {
		writer.Header().Set("Retry-After", "5")
		writeJSON(
			writer,
			http.StatusTooManyRequests,
			map[string]any{errorKey: "backup already in progress"},
		)

		return
	}
	defer handler.backupMu.Unlock()

	dir, err := os.MkdirTemp("", "portfolio-backup-")
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	defer func() {
		_ = os.RemoveAll(dir)
	}()

	backupPath := filepath.Join(dir, "backup.db")

	err = handler.settingsSvc.Backup(request.Context(), backupPath)
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	filename := "portfolio-backup-" + time.Now().UTC().Format("20060102-150405") + ".db"
	sendAttachment(writer, request, backupPath, filename)
}

// clearAllData deletes all user data and asks the browser to reload the page.
func (handler *Handler) clearAllData(writer http.ResponseWriter, request *http.Request,
) {
	//nolint:contextcheck // The reset callback intentionally closes over this request context.
	err := handler.withPriceCacheReset(request.Context(), func() error {
		return handler.settingsSvc.ClearAllData(request.Context())
	})
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	writer.Header().Set("Hx-Refresh", "true")
	writer.WriteHeader(http.StatusOK)
}

// Price Sources API.
func (handler *Handler) getProviders(writer http.ResponseWriter, request *http.Request,
) {
	health, err := handler.priceSvc.GetProviderHealthContext(request.Context())
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	writeJSON(writer, http.StatusOK, health)
}

func (handler *Handler) getProviderMapping(writer http.ResponseWriter, request *http.Request,
) {
	mapping, err := handler.priceSvc.GetHoldingProviderMappingContext(request.Context())
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	writeJSON(writer, http.StatusOK, mapping)
}

func (handler *Handler) testProvider(writer http.ResponseWriter, request *http.Request,
) {
	requestCtx := request.Context()

	var req struct {
		Provider     string `json:"provider"`
		InstrumentID string `json:"instrumentId"`
	}

	err := decodeJSON(request, &req)
	if err != nil {
		respondJSONDecodeError(writer, err)

		return
	}

	priceData, err := handler.priceSvc.TestProvider(requestCtx, req.Provider, req.InstrumentID)
	if err != nil {
		writeJSON(writer, http.StatusOK, map[string]any{
			"success": false,
			"error":   err.Error(),
		})

		return
	}

	writeJSON(writer, http.StatusOK, map[string]any{
		"success":    true,
		"price":      priceData.Value,
		"currency":   priceData.Currency,
		"source":     priceData.Source,
		"fetched_at": priceData.FetchedAt.Format(time.RFC3339),
	})
}

func (handler *Handler) refreshPrices(writer http.ResponseWriter, request *http.Request,
) {
	requestCtx := request.Context()

	holdings, err := handler.holdingSvc.GetAll(requestCtx)
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	err = handler.priceSvc.RefreshAll(requestCtx, holdings)
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	writer.WriteHeader(http.StatusOK)
}

func (handler *Handler) updateHoldingProvider(writer http.ResponseWriter, request *http.Request,
) {
	id, err := strconv.Atoi(request.PathValue("id"))
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: invalidIDMsg})

		return
	}

	var req struct {
		Provider     *string `json:"provider"`
		InstrumentID *string `json:"instrumentId"`
	}

	err = decodeJSON(request, &req)
	if err != nil {
		respondJSONDecodeError(writer, err)

		return
	}

	if req.Provider == nil || req.InstrumentID == nil {
		writeJSON(
			writer,
			http.StatusBadRequest,
			map[string]any{
				errorKey: "provider and instrumentId must be explicitly supplied strings",
			},
		)

		return
	}

	err = handler.priceSvc.UpdateHoldingProviderContext(
		request.Context(),
		id,
		*req.Provider,
		*req.InstrumentID,
	)
	if err != nil {
		respondHoldingMutationError(writer, request, err)

		return
	}

	writer.WriteHeader(http.StatusOK)
}

// Settings API.
func (handler *Handler) getSettings(writer http.ResponseWriter, request *http.Request,
) {
	settings, err := handler.settingsSvc.GetAll(request.Context())
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	writeJSON(writer, http.StatusOK, settings)
}

func (handler *Handler) updateCurrency(writer http.ResponseWriter, request *http.Request,
) {
	requestCtx := request.Context()

	var req struct {
		Currency string `json:"currency"`
	}

	err := decodeJSON(request, &req)
	if err != nil {
		respondJSONDecodeError(writer, err)

		return
	}

	if !supportedCurrency(req.Currency) {
		writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: "invalid currency"})

		return
	}

	err = handler.settingsSvc.SetDisplayCurrency(requestCtx, req.Currency)
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	writer.WriteHeader(http.StatusOK)
}

func (handler *Handler) updateDarkMode(writer http.ResponseWriter, request *http.Request,
) {
	requestCtx := request.Context()

	var req struct {
		Mode string `json:"mode"`
	}

	err := decodeJSON(request, &req)
	if err != nil {
		respondJSONDecodeError(writer, err)

		return
	}

	if req.Mode != "system" && req.Mode != "light" && req.Mode != "dark" {
		writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: "invalid mode"})

		return
	}

	err = handler.settingsSvc.SetDarkMode(requestCtx, req.Mode)
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	writer.WriteHeader(http.StatusOK)
}

func (handler *Handler) updateRefreshInterval(writer http.ResponseWriter, request *http.Request,
) {
	requestCtx := request.Context()

	var req struct {
		Interval int `json:"interval"`
	}

	err := decodeJSON(request, &req)
	if err != nil {
		respondJSONDecodeError(writer, err)

		return
	}

	if req.Interval < 10 || req.Interval > 3600 {
		writeJSON(
			writer,
			http.StatusBadRequest,
			map[string]any{errorKey: "interval must be between 10 and 3600 seconds"},
		)

		return
	}

	err = handler.settingsSvc.SetRefreshInterval(requestCtx, req.Interval)
	if err != nil {
		respondInternalError(writer, request, err)

		return
	}

	writer.WriteHeader(http.StatusOK)
}

// Page handlers (server-rendered with HTMX).
func (handler *Handler) pageDashboard(writer http.ResponseWriter, request *http.Request,
) {
	requestCtx := request.Context()
	displayCurrency := handler.pageCurrency(request)

	data, err := handler.holdingSvc.GetDashboardData(requestCtx, displayCurrency)
	status := http.StatusOK

	if err != nil {
		slog.ErrorContext(requestCtx, "Dashboard valuation unavailable", slog.Any("error", err))

		status = http.StatusServiceUnavailable
	}

	handler.renderPageStatus(writer, request, status, "dashboard.html", map[string]any{
		"ValuationUnavailable": err != nil,
		"Data":                 data,
		"DisplayCurrency":      displayCurrency,
		"Title":                titleDashboard, //nolint:goconst // Map key is required
	})
}

func (handler *Handler) pageHoldings(writer http.ResponseWriter, request *http.Request,
) {
	requestCtx := request.Context()
	displayCurrency := handler.pageCurrency(request)

	holdings, err := handler.holdingSvc.GetWithPrices(requestCtx, displayCurrency)
	valuationUnavailable := err != nil
	holdingsUnavailable := false
	status := http.StatusOK

	if valuationUnavailable {
		slog.ErrorContext(requestCtx, "Holdings valuation unavailable", slog.Any("error", err))

		status = http.StatusServiceUnavailable
		holdings = nil

		// Keep saved metadata editable without presenting incomplete valuations.
		savedHoldings, listErr := handler.holdingSvc.GetAll(requestCtx)
		if listErr != nil {
			slog.ErrorContext(
				requestCtx,
				"Holdings metadata unavailable",
				slog.Any("error", listErr),
			)

			holdingsUnavailable = true
		} else {
			for index := range savedHoldings {
				holdings = append(holdings, models.HoldingWithPrice{Holding: savedHoldings[index]})
			}
		}
	}

	baskets, err := handler.holdingSvc.GetUniqueBaskets(requestCtx)
	if err != nil {
		baskets = []string{}
	}

	locations, err := handler.holdingSvc.GetUniqueLocations(requestCtx)
	if err != nil {
		locations = []string{}
	}

	coverage := 0
	valued := 0

	for i := range holdings {
		if holdings[i].ValuationAvailable || holdings[i].Quantity == 0 {
			valued++
		}

		if holdings[i].EstimatedValuationAvailable {
			coverage++
		}
	}

	handler.renderPageStatus(writer, request, status, "holdings.html", map[string]any{
		"ValuationUnavailable": valuationUnavailable,
		"HoldingsUnavailable":  holdingsUnavailable,
		"Holdings":             holdings,
		"CoverageCount":        coverage,
		"ValuedCount":          valued,
		"UnvaluedCount":        len(holdings) - valued,
		"TotalHoldings":        len(holdings),
		"DisplayCurrency":      displayCurrency,
		"Baskets":              baskets,
		"Locations":            locations,
		"Title":                titleHoldings,
	})
}

func (handler *Handler) pageHistory(writer http.ResponseWriter, request *http.Request,
) {
	requestCtx := request.Context()
	displayCurrency := handler.pageCurrency(request)

	days, err := strconv.Atoi(queryDefault(request, "days", "365"))
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

	// Hardcoded from the model's AssetType constants.
	assetTypes := []string{"Stock", "ETF", "TR Fund", "Crypto", "Gold", "Cash"}

	handler.renderPage(writer, request, "history.html", map[string]any{
		"ChartData":       chartData,
		"DisplayCurrency": displayCurrency,
		"Days":            days,
		"Baskets":         baskets,
		"Locations":       locations,
		"AssetTypes":      assetTypes,
		"Title":           titleHistory,
	})
}

func (handler *Handler) pageSources(writer http.ResponseWriter, request *http.Request,
) {
	providers, err := handler.priceSvc.GetProviderHealthContext(request.Context())
	if err != nil {
		providers = []models.ProviderHealth{}
	}

	mapping, err := handler.priceSvc.GetHoldingProviderMappingContext(request.Context())
	if err != nil {
		mapping = []price.HoldingProviderMapping{}
	}

	handler.renderPage(writer, request, "sources.html", map[string]any{
		"Providers": providers,
		"Mapping":   mapping,
		"Title":     titleSources,
	})
}

func (handler *Handler) pageSettings(writer http.ResponseWriter, request *http.Request,
) {
	settings, err := handler.settingsSvc.GetAll(request.Context())
	if err != nil {
		settings = models.Settings{} //nolint:exhaustruct_v5 // Empty struct for error fallback
	}

	handler.renderPage(writer, request, "settings.html", map[string]any{
		"Settings": settings,
		"Title":    titleSettings,
	})
}
