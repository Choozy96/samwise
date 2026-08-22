package web

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"

	"samwise/internal/store"
)

//go:embed templates/*.html
var templatesFS embed.FS

// tmpl carries a small FuncMap: appVersion lets any template (e.g. the footer)
// show the build version without every handler threading it through pageData;
// tokens renders token counts compactly (12.4k / 1.3M) for the usage panel.
var tmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"appVersion": func() string { return appVersion },
	"tokens":     store.HumanTokens,
}).ParseFS(templatesFS, "templates/*.html"))

// pageData is the data passed to a template. Common keys (User, Title, Flash,
// FlashKind) are filled by render if absent.
type pageData map[string]any

// render executes the named template into a buffer first, so a template error
// never produces a half-written response.
func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data pageData) {
	if data == nil {
		data = pageData{}
	}
	if _, ok := data["User"]; !ok {
		data["User"] = currentUser(r.Context())
	}
	if _, ok := data["Title"]; !ok {
		data["Title"] = name
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		s.log.Error("template render failed", "template", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}
