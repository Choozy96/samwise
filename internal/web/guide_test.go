package web

import (
	"strings"
	"testing"
)

// TestGuideAdminSplit: SetUserGuide splits the admin appendix off at the marker
// — the general part excludes admin content, the admin part includes it.
func TestGuideAdminSplit(t *testing.T) {
	md := "# Guide\n\nGeneral stuff for everyone.\n\n" +
		adminGuideMarker + "\n\n## Admin\n\nSecret admin stuff.\n"
	// Reset globals after the test so other tests see the real guide.
	origGeneral, origAdmin := guideHTML, adminGuideHTML
	t.Cleanup(func() { guideHTML, adminGuideHTML = origGeneral, origAdmin })

	SetUserGuide(md)

	if g := string(guideHTML); !strings.Contains(g, "General stuff") || strings.Contains(g, "Secret admin stuff") {
		t.Errorf("general guide should exclude admin content:\n%s", g)
	}
	if a := string(adminGuideHTML); !strings.Contains(a, "Secret admin stuff") || !strings.Contains(a, "Admin") {
		t.Errorf("admin appendix missing content:\n%s", a)
	}
	// The marker itself must not leak into the general HTML.
	if strings.Contains(string(guideHTML), "ADMIN GUIDE") {
		t.Error("split marker leaked into the general guide")
	}
}

// TestGuideNoMarker: a guide without the marker renders entirely as general,
// with no admin appendix.
func TestGuideNoMarker(t *testing.T) {
	origGeneral, origAdmin := guideHTML, adminGuideHTML
	t.Cleanup(func() { guideHTML, adminGuideHTML = origGeneral, origAdmin })
	adminGuideHTML = ""
	SetUserGuide("# Guide\n\nOnly general content.\n")
	if !strings.Contains(string(guideHTML), "Only general content") {
		t.Error("general content missing")
	}
	if adminGuideHTML != "" {
		t.Errorf("no admin appendix expected, got: %s", adminGuideHTML)
	}
}
