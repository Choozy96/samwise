package mcpserver

import (
	"strings"
	"testing"

	"samwise/internal/store"
)

func TestSetSkillAudience(t *testing.T) {
	h, ctx := newJobHandlers(t)
	if _, err := h.db.CreateSkill(ctx, store.Skill{UserID: h.userID, Name: "lookup", Content: "do a lookup", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	r, _, _ := h.setSkillAudience(ctx, nil, setSkillAudienceIn{Name: "lookup", Audience: "everyone"})
	if s := resultText(r); !strings.Contains(s, "everyone") {
		t.Fatalf("set everyone: %q", s)
	}
	if sk, _ := h.db.GetSkillByName(ctx, h.userID, "lookup"); sk == nil || !sk.OpenToEveryone() {
		t.Errorf("skill audience not persisted as everyone")
	}
	// Back to paired.
	h.setSkillAudience(ctx, nil, setSkillAudienceIn{Name: "lookup", Audience: "paired"})
	if sk, _ := h.db.GetSkillByName(ctx, h.userID, "lookup"); sk == nil || sk.OpenToEveryone() {
		t.Errorf("skill audience not reset to paired")
	}
	// Bad value.
	r, _, _ = h.setSkillAudience(ctx, nil, setSkillAudienceIn{Name: "lookup", Audience: "public"})
	if s := resultText(r); !strings.Contains(s, "must be") {
		t.Errorf("bad audience should fail: %q", s)
	}
}

func TestSetToolAudience(t *testing.T) {
	h, ctx := newJobHandlers(t)

	// Open WebSearch to everyone.
	r, _, _ := h.setToolAudience(ctx, nil, setToolAudienceIn{Tool: "WebSearch", Audience: "everyone"})
	if s := resultText(r); !strings.Contains(s, "everyone") {
		t.Fatalf("open WebSearch: %q", s)
	}
	st, _ := h.db.GetSettings(ctx, h.userID)
	if store.ParseToolAudience(st.ToolAudience)["WebSearch"] != store.AudienceEveryone {
		t.Errorf("WebSearch audience not persisted: %q", st.ToolAudience)
	}

	// Exec tools are hard-locked by default (ALLOW_EXEC_TOOL_OPENING off).
	for _, tool := range []string{"Bash", "Write", "Edit"} {
		r, _, _ = h.setToolAudience(ctx, nil, setToolAudienceIn{Tool: tool, Audience: "everyone"})
		if s := resultText(r); !strings.Contains(s, "can't be opened") {
			t.Errorf("%s should be refused by default: %q", tool, s)
		}
	}

	// Unknown tool.
	r, _, _ = h.setToolAudience(ctx, nil, setToolAudienceIn{Tool: "Telepathy", Audience: "everyone"})
	if s := resultText(r); !strings.Contains(s, "unknown tool") {
		t.Errorf("unknown tool should fail: %q", s)
	}

	// Reset WebSearch to paired clears the override (empty map → "").
	h.setToolAudience(ctx, nil, setToolAudienceIn{Tool: "WebSearch", Audience: "paired"})
	st, _ = h.db.GetSettings(ctx, h.userID)
	if st.ToolAudience != "" {
		t.Errorf("resetting to paired should clear the override, got %q", st.ToolAudience)
	}
}

// TestSetToolAudienceExecOpening covers the dangerous opt-in: when the deployment
// allows it, an exec tool can be opened to everyone (with a loud warning).
func TestSetToolAudienceExecOpening(t *testing.T) {
	h, ctx := newJobHandlers(t)
	SetAllowExecToolOpening(true)
	t.Cleanup(func() { SetAllowExecToolOpening(false) })

	r, _, _ := h.setToolAudience(ctx, nil, setToolAudienceIn{Tool: "Bash", Audience: "everyone"})
	s := resultText(r)
	if !strings.Contains(s, "WARNING") || !strings.Contains(s, "EVERYONE") {
		t.Errorf("opening Bash to everyone should carry a loud warning: %q", s)
	}
	st, _ := h.db.GetSettings(ctx, h.userID)
	if store.ParseToolAudience(st.ToolAudience)["Bash"] != store.AudienceEveryone {
		t.Errorf("Bash audience not persisted with opening enabled: %q", st.ToolAudience)
	}
}

func TestAudienceReadOnlyDenied(t *testing.T) {
	h, ctx := newJobHandlers(t)
	h.readOnly = true
	r1, _, _ := h.setSkillAudience(ctx, nil, setSkillAudienceIn{Name: "x", Audience: "everyone"})
	r2, _, _ := h.setToolAudience(ctx, nil, setToolAudienceIn{Tool: "WebSearch", Audience: "everyone"})
	if s := resultText(r1); !strings.Contains(s, "Not permitted") {
		t.Errorf("set_skill_audience should be denied read-only: %q", s)
	}
	if s := resultText(r2); !strings.Contains(s, "Not permitted") {
		t.Errorf("set_tool_audience should be denied read-only: %q", s)
	}
}
