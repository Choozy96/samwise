package web

import (
	"bytes"
	"strings"
	"testing"
)

// TestFooterShowsVersion: the footer (on every page) renders the build version
// via the appVersion template func, so any user can see it.
func TestFooterShowsVersion(t *testing.T) {
	orig := appVersion
	t.Cleanup(func() { appVersion = orig })
	SetVersion("v0.3.6")

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "footer", pageData{}); err != nil {
		t.Fatalf("footer render: %v", err)
	}
	if !strings.Contains(buf.String(), "Samwise v0.3.6") {
		t.Errorf("footer missing version; got:\n%s", buf.String())
	}
}
