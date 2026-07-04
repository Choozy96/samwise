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
	if got := off.builtinTools(&store.Settings{ExtraTools: "WebFetch,Task"}, false); got != nil {
		t.Errorf("master switch off must yield no tools, got %v", got)
	}

	on := &Orchestrator{cfg: &config.Config{AllowAgentTools: true}}

	// Default (no extras): the scoped file/shell set only.
	base := on.builtinTools(&store.Settings{}, false)
	if !hasTool(base, "Bash") || !hasTool(base, "Read") {
		t.Errorf("default should include the scoped set: %v", base)
	}
	if hasTool(base, "WebFetch") || hasTool(base, "Task") {
		t.Errorf("default should NOT include any optional tools: %v", base)
	}

	// Opt-in adds exactly the chosen tools; unknown names are dropped.
	sel := on.builtinTools(&store.Settings{ExtraTools: "WebFetch, WebSearch , NotARealTool"}, false)
	if !hasTool(sel, "Bash") || !hasTool(sel, "WebFetch") || !hasTool(sel, "WebSearch") {
		t.Errorf("selected tools should be added to the scoped set: %v", sel)
	}
	if hasTool(sel, "Task") || hasTool(sel, "NotARealTool") {
		t.Errorf("only the selected, catalog-valid tools should appear: %v", sel)
	}

	// Read-only run (unregistered group sender): by default, read tools only — no
	// Bash/Write/Edit, and no opt-in extras (which default to 'paired').
	ro := on.builtinTools(&store.Settings{ExtraTools: "WebFetch"}, true)
	if !hasTool(ro, "Read") || !hasTool(ro, "Grep") {
		t.Errorf("read-only should keep file reads: %v", ro)
	}
	for _, w := range []string{"Bash", "Write", "Edit", "WebFetch"} {
		if hasTool(ro, w) {
			t.Errorf("read-only must not include write-capable/opt-in tool %q: %v", w, ro)
		}
	}
}

// TestBuiltinToolsAudience covers the per-tool audience override on read-only
// runs: a tool opened to 'everyone' becomes available, exec tools never do, and
// a read tool locked to 'paired' is removed.
func TestBuiltinToolsAudience(t *testing.T) {
	on := &Orchestrator{cfg: &config.Config{AllowAgentTools: true}}

	// WebSearch enabled + opened to everyone → available read-only; WebFetch still
	// paired-only (default) → not; Bash never appears.
	s := &store.Settings{
		ExtraTools:   "WebSearch,WebFetch",
		ToolAudience: `{"WebSearch":"everyone","Bash":"everyone"}`, // Bash override must be ignored
	}
	ro := on.builtinTools(s, true)
	if !hasTool(ro, "WebSearch") {
		t.Errorf("WebSearch opened to everyone should be available read-only: %v", ro)
	}
	if hasTool(ro, "WebFetch") {
		t.Errorf("WebFetch (default paired) must not be available read-only: %v", ro)
	}
	if hasTool(ro, "Bash") {
		t.Errorf("Bash is hard-locked and must never be available read-only, even if set: %v", ro)
	}
	if !hasTool(ro, "Read") {
		t.Errorf("Read should still default to everyone: %v", ro)
	}

	// Locking Read to 'paired' removes it from a read-only run.
	s2 := &store.Settings{ToolAudience: `{"Read":"paired"}`}
	ro2 := on.builtinTools(s2, true)
	if hasTool(ro2, "Read") {
		t.Errorf("Read locked to paired must be removed from a read-only run: %v", ro2)
	}
	if !hasTool(ro2, "Grep") {
		t.Errorf("Grep should remain (still default everyone): %v", ro2)
	}

	// A paired run is unaffected by audience — gets the full set.
	full := on.builtinTools(s, false)
	if !hasTool(full, "Bash") || !hasTool(full, "WebFetch") || !hasTool(full, "WebSearch") {
		t.Errorf("paired run should get the full set regardless of audience: %v", full)
	}

	// With ALLOW_EXEC_TOOL_OPENING on, Bash set to everyone DOES reach a read-only
	// run (the dangerous opt-in). Write left at default (paired) still doesn't.
	danger := &Orchestrator{cfg: &config.Config{AllowAgentTools: true, AllowExecToolOpening: true}}
	roEx := danger.builtinTools(&store.Settings{ToolAudience: `{"Bash":"everyone"}`}, true)
	if !hasTool(roEx, "Bash") {
		t.Errorf("with exec-opening on, Bash@everyone should reach a read-only run: %v", roEx)
	}
	if hasTool(roEx, "Write") {
		t.Errorf("Write (default paired) should still be excluded: %v", roEx)
	}
}
