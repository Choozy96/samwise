package web

import (
	"bytes"
	"html/template"
	"net/http"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// adminGuideMarker splits the guide: everything after it is the admin-only
// appendix, rendered separately and shown only to admins.
const adminGuideMarker = "<!-- ADMIN GUIDE -->"

// guideHTML is the general user guide; adminGuideHTML is the admin appendix.
// Both are rendered once at startup.
var (
	guideHTML      = template.HTML("<p>Guide not loaded.</p>")
	adminGuideHTML template.HTML
)

// SetUserGuide renders the (trusted, embedded) guide markdown to HTML, splitting
// off the admin-only appendix at adminGuideMarker. Called once at startup with
// the embedded docs/user-guide.md.
func SetUserGuide(md string) {
	if md == "" {
		return
	}
	general, admin := md, ""
	if i := strings.Index(md, adminGuideMarker); i >= 0 {
		general, admin = md[:i], md[i+len(adminGuideMarker):]
	}
	guideHTML = renderMarkdown(general)
	if strings.TrimSpace(admin) != "" {
		adminGuideHTML = renderMarkdown(admin)
	}
}

// renderMarkdown converts trusted, embedded markdown to HTML.
func renderMarkdown(md string) template.HTML {
	gm := goldmark.New(goldmark.WithExtensions(extension.GFM))
	var buf bytes.Buffer
	if err := gm.Convert([]byte(md), &buf); err != nil {
		return ""
	}
	// Safe: the source is our own embedded markdown, not user input.
	return template.HTML(buf.String())
}

// handleGuide renders the in-app user guide. The admin appendix is included only
// for admins.
func (s *Server) handleGuide(w http.ResponseWriter, r *http.Request) {
	data := pageData{"Title": "Guide", "GuideHTML": guideHTML}
	if u := currentUser(r.Context()); u != nil && u.IsAdmin {
		data["AdminGuideHTML"] = adminGuideHTML
	}
	s.render(w, r, "guide", data)
}
