package handlers

import (
	"crypto/sha256"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/render"
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

//nolint:ireturn // Signature required by Gin's render.HTMLRender interface.
func (r *pageRenderer) Instance(name string, data any) render.Render {
	return render.HTML{Template: r.pages[name], Name: name, Data: data}
}

func (handler *Handler) pageCurrency(ctx *gin.Context) string {
	currency := ctx.Query("currency")
	if currency == currencyTRY || currency == currencyUSD {
		return currency
	}

	settings, err := handler.settingsSvc.GetAll(ctx.Request.Context())
	if err == nil && settings.DisplayCurrency == currencyUSD {
		return currencyUSD
	}

	return currencyTRY
}

func (handler *Handler) renderPage(ctx *gin.Context, name string, data gin.H) {
	settings, err := handler.settingsSvc.GetAll(ctx.Request.Context())
	if err == nil {
		data["Theme"] = settings.DarkMode
		data["Settings"] = settings
	}

	data["DisplayCurrency"] = handler.pageCurrency(ctx)
	data["ActivePage"] = strings.TrimSuffix(name, ".html")
	ctx.HTML(http.StatusOK, name, data)
}
