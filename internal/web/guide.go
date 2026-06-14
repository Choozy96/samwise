package web

import (
	"bytes"
	"html/template"
	"net/http"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// guideHTML is the user guide rendered once at startup.
var guideHTML = template.HTML("<p>Guide not loaded.</p>")

// SetUserGuide renders the (trusted, embedded) guide markdown to HTML. Called
// once at startup with the embedded docs/user-guide.md.
func SetUserGuide(md string) {
	if md == "" {
		return
	}
	gm := goldmark.New(goldmark.WithExtensions(extension.GFM))
	var buf bytes.Buffer
	if err := gm.Convert([]byte(md), &buf); err != nil {
		return
	}
	// Safe: the source is our own embedded markdown, not user input.
	guideHTML = template.HTML(buf.String())
}

// handleGuide renders the in-app user guide (spec §13).
func (s *Server) handleGuide(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "guide", pageData{"Title": "Guide", "GuideHTML": guideHTML})
}
