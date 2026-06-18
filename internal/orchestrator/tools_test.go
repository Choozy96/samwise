package orchestrator

import (
	"testing"

	"samwise/internal/config"
	"samwise/internal/store"
)

func hasTool(list []string, name string) bool {
	for _, t := range list {
		if t == name {
			return true
		}
	}
	return false
}

// TestBuiltinTools covers tool selection: master switch off => nothing; on =>
// the scoped set plus exactly the user's opted-in extras, and only catalog tools.
func TestBuiltinTools(t *testing.T) {
	off := &Orchestrator{cfg: &config.Config{AllowAgentTools: false}}
	if got := off.builtinTools(&store.Settings{ExtraTools: "WebFetch,Task"}); got != nil {
		t.Errorf("master switch off must yield no tools, got %v", got)
	}

	on := &Orchestrator{cfg: &config.Config{AllowAgentTools: true}}

	// Default (no extras): the scoped file/shell set only.
	base := on.builtinTools(&store.Settings{})
	if !hasTool(base, "Bash") || !hasTool(base, "Read") {
		t.Errorf("default should include the scoped set: %v", base)
	}
	if hasTool(base, "WebFetch") || hasTool(base, "Task") {
		t.Errorf("default should NOT include any optional tools: %v", base)
	}

	// Opt-in adds exactly the chosen tools; unknown names are dropped.
	sel := on.builtinTools(&store.Settings{ExtraTools: "WebFetch, WebSearch , NotARealTool"})
	if !hasTool(sel, "Bash") || !hasTool(sel, "WebFetch") || !hasTool(sel, "WebSearch") {
		t.Errorf("selected tools should be added to the scoped set: %v", sel)
	}
	if hasTool(sel, "Task") || hasTool(sel, "NotARealTool") {
		t.Errorf("only the selected, catalog-valid tools should appear: %v", sel)
	}
}
