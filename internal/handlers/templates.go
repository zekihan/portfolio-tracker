package handlers

import (
	"html/template"
	"path/filepath"

	"github.com/gin-gonic/gin/render"
)

type pageRenderer struct {
	pages map[string]*template.Template
}

func newPageRenderer(funcs template.FuncMap) *pageRenderer {
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
