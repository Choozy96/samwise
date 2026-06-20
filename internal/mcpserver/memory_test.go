package mcpserver

import (
	"fmt"
	"strings"
	"testing"
)

// TestMemoryForgetBothLayers covers the fix that memory_forget can delete an
// episodic (dated) note, not just a semantic fact — and that memory_search
// surfaces the id + layer the agent needs to do it.
func TestMemoryForgetBothLayers(t *testing.T) {
	h, ctx := newJobHandlers(t)

	semID, err := h.db.SaveSemantic(ctx, h.userID, 0, "drink", "preference", "alice prefers tea", "assistant")
	if err != nil {
		t.Fatal(err)
	}
	epiID, err := h.db.SaveEpisodic(ctx, h.userID, "day", "2026-06-15", "alice shipped the widget")
	if err != nil {
		t.Fatal(err)
	}

	// memory_search must show the id + layer for BOTH layers.
	res, _, _ := h.memorySearch(ctx, nil, memorySearchIn{Query: "tea"})
	if got := resultText(res); !strings.Contains(got, fmt.Sprintf("id=%d", semID)) || !strings.Contains(got, "layer=semantic") {
		t.Errorf("search should show semantic id+layer: %q", got)
	}
	res, _, _ = h.memorySearch(ctx, nil, memorySearchIn{Query: "widget"})
	if got := resultText(res); !strings.Contains(got, fmt.Sprintf("id=%d", epiID)) || !strings.Contains(got, "layer=episodic") {
		t.Errorf("search should show episodic id+layer: %q", got)
	}

	// Forget the episodic note explicitly — previously impossible via MCP.
	res, _, _ = h.memoryForget(ctx, nil, memoryForgetIn{ID: epiID, Layer: "episodic"})
	if res.IsError || !strings.Contains(resultText(res), "Deleted") {
		t.Errorf("forget episodic failed: %q", resultText(res))
	}
	// It's gone now (a second attempt is a normal not_found, not an error).
	res, _, _ = h.memoryForget(ctx, nil, memoryForgetIn{ID: epiID, Layer: "episodic"})
	if res.IsError || !strings.Contains(resultText(res), "No episodic entry") {
		t.Errorf("episodic should be gone: %q", resultText(res))
	}

	// Forget the semantic fact with no layer given — the fallback finds it.
	res, _, _ = h.memoryForget(ctx, nil, memoryForgetIn{ID: semID})
	if res.IsError || !strings.Contains(resultText(res), "Deleted") {
		t.Errorf("forget semantic (no layer) failed: %q", resultText(res))
	}

	// A truly missing id is a not_found result, not an error.
	res, _, _ = h.memoryForget(ctx, nil, memoryForgetIn{ID: 99999})
	if res.IsError {
		t.Errorf("missing id should be a normal result, not an error: %q", resultText(res))
	}
}

// TestMemorySearchUserScoped confirms the MCP handler only ever returns the
// bound user's memory — a handler for one user can't surface another's, even
// when the query would match the other user's content.
func TestMemorySearchUserScoped(t *testing.T) {
	h, ctx := newJobHandlers(t) // h.userID is the handler's bound user
	bob, err := h.db.CreateUser(ctx, "bob", "h", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.SaveSemantic(ctx, bob, 0, "t", "fact", "bob private nuclear codes", "assistant"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.SaveSemantic(ctx, h.userID, 0, "t", "fact", "my own note", "assistant"); err != nil {
		t.Fatal(err)
	}

	res, _, _ := h.memorySearch(ctx, nil, memorySearchIn{Query: "private nuclear codes note"})
	got := resultText(res)
	if strings.Contains(got, "bob") || strings.Contains(got, "nuclear") {
		t.Errorf("memory_search leaked another user's memory: %q", got)
	}
}

// TestGetSettingsNoVestigialBriefing checks get_settings drops the removed
// briefing_time and exposes the real distillation_time instead.
func TestGetSettingsNoVestigialBriefing(t *testing.T) {
	h, ctx := newJobHandlers(t)
	res, _, _ := h.getSettings(ctx, nil, emptyIn{})
	got := resultText(res)
	if strings.Contains(got, "briefing_time") {
		t.Errorf("get_settings should not expose vestigial briefing_time: %q", got)
	}
	if !strings.Contains(got, "distillation_time") {
		t.Errorf("get_settings should expose distillation_time: %q", got)
	}
}
