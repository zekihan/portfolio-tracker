package handlers

import (
	"crypto/sha256"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
)

const (
	currencyTRY = "TRY"
	currencyUSD = "USD"
)

type pageRenderer struct {
	pages map[string]*template.Template
}

func newPageRenderer(funcs template.FuncMap) *pageRenderer {
	assets := make(map[string]string)

	for _, name := range []string{"app.js", "style.css", "chart.min.js"} {
		content, err := os.ReadFile(filepath.Join("web", "static", name))
		if err != nil {
			panic(err)
		}

		digest := sha256.Sum256(content)
		assets[name] = fmt.Sprintf("/static/%s?v=%x", name, digest[:8])
	}

	funcs["assetURL"] = func(name string) string { return assets[name] }

	base := template.Must(
		template.New("base.html").Funcs(funcs).ParseFiles("web/templates/base.html"),
	)
	pages := make(map[string]*template.Template)

	for _, name := range []string{"dashboard.html", "holdings.html", "history.html", "sources.html", "settings.html"} {
		page := template.Must(base.Clone())
		pages[name] = template.Must(page.ParseFiles(filepath.Join("web", "templates", name)))
	}

	for _, name := range []string{"holding-form", "manual-price"} {
		pages[name] = template.Must(template.New(name).Funcs(funcs).ParseFiles(
			filepath.Join("web", "templates", "partials", name+".html")))
	}

	return &pageRenderer{pages: pages}
}

func (renderer *pageRenderer) render(
	writer http.ResponseWriter,
	status int,
	name string,
	data any,
) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.WriteHeader(status)

	err := renderer.pages[name].ExecuteTemplate(writer, name, data)
	if err != nil {
		slog.Error(
			"Template rendering failed",
			slog.String("template", name),
			slog.Any("error", err),
		)
	}
}

func (handler *Handler) pageCurrency(request *http.Request) string {
	currency := request.URL.Query().Get("currency")
	if currency == currencyTRY || currency == currencyUSD {
		return currency
	}

	settings, err := handler.settingsSvc.GetAll(request.Context())
	if err == nil && settings.DisplayCurrency == currencyUSD {
		return currencyUSD
	}

	return currencyTRY
}

func (handler *Handler) renderPage(writer http.ResponseWriter, request *http.Request,
	name string, data map[string]any,
) {
	handler.renderPageStatus(writer, request, http.StatusOK, name, data)
}

func (handler *Handler) renderPageStatus(writer http.ResponseWriter, request *http.Request,
	status int, name string, data map[string]any,
) {
	settings, err := handler.settingsSvc.GetAll(request.Context())
	if err == nil {
		data["Theme"] = settings.DarkMode
		data["Settings"] = settings
	}

	data["DisplayCurrency"] = handler.pageCurrency(request)
	data["BuildInfo"] = "Development build"

	if build, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range build.Settings {
			if setting.Key == "vcs.revision" {
				data["BuildInfo"] = setting.Value
			}
		}
	}

	data["ActivePage"] = strings.TrimSuffix(name, ".html")
	handler.renderer.
		render(writer, status, name, data)
}
