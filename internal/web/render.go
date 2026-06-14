package web

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
)

//go:embed templates/*.html
var templatesFS embed.FS

var tmpl = template.Must(template.New("").ParseFS(templatesFS, "templates/*.html"))

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
