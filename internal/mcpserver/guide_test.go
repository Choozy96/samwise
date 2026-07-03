package mcpserver

import (
	"strings"
	"testing"
)

func TestSplitGuideSections(t *testing.T) {
	g := "# Title\nintro line\n\n## Alpha\na1\na2\n\n## Beta\nb1\n"
	secs := splitGuideSections(g)
	if len(secs) != 2 {
		t.Fatalf("want 2 sections, got %d", len(secs))
	}
	if secs[0].title != "Alpha" || !strings.Contains(secs[0].body, "a1") || !strings.Contains(secs[0].body, "a2") {
		t.Errorf("alpha section wrong: %+v", secs[0])
	}
	if secs[1].title != "Beta" || strings.Contains(secs[1].body, "a1") {
		t.Errorf("beta section bled content: %+v", secs[1])
	}
	if strings.Contains(secs[0].body, "intro line") {
		t.Error("pre-## intro must not be in the first section")
	}
	if s := splitGuideSections("   "); s != nil {
		t.Errorf("blank guide should yield no sections, got %d", len(s))
	}
}

// TestReadGuideTool exercises the agent-facing tool: no arg lists sections, a
// section name returns that section, an unknown name falls back to the list.
func TestReadGuideTool(t *testing.T) {
	h, ctx := newJobHandlers(t)
	SetUserGuide("# Guide\n\n## Memory\nhow memory works\n\n## Cron jobs\nhow cron works\n")
	t.Cleanup(func() { SetUserGuide("") })

	toc, _, _ := h.readGuide(ctx, nil, readGuideIn{})
	if s := resultText(toc); !strings.Contains(s, "Memory") || !strings.Contains(s, "Cron jobs") {
		t.Errorf("TOC missing sections: %q", s)
	}

	sec, _, _ := h.readGuide(ctx, nil, readGuideIn{Section: "cron"})
	if s := resultText(sec); !strings.Contains(s, "how cron works") || strings.Contains(s, "how memory works") {
		t.Errorf("section fetch wrong: %q", s)
	}

	miss, _, _ := h.readGuide(ctx, nil, readGuideIn{Section: "nope"})
	if s := resultText(miss); !strings.Contains(s, "No section matched") || !strings.Contains(s, "Memory") {
		t.Errorf("unknown section should list options: %q", s)
	}
}
