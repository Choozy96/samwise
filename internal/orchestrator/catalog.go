package orchestrator

import "strings"

// This file defines the catalogs of selectable models and access methods
// (runtimes), shared by the settings UI and the slash-command handler so both
// agree on names, aliases, and labels.

// ModelOption is a selectable chat model.
type ModelOption struct {
	Alias string // short name for /model and form values: "", opus, sonnet, haiku
	ID    string // the model id passed to the runtime ("" = runtime default)
	Label string // human label for the UI
}

// Models lists the built-in Claude model choices. The empty alias is "default".
// (When the codex/channels runtimes land, their model lists differ; this set is
// for the Claude runtimes that ship today.)
var Models = []ModelOption{
	{Alias: "default", ID: "", Label: "Default (runtime's default model)"},
	{Alias: "opus", ID: "claude-opus-4-8", Label: "Claude Opus 4.8"},
	{Alias: "sonnet", ID: "claude-sonnet-4-6", Label: "Claude Sonnet 4.6"},
	{Alias: "haiku", ID: "claude-haiku-4-5-20251001", Label: "Claude Haiku 4.5"},
}

// RuntimeOption is a selectable access method.
type RuntimeOption struct {
	ID    string   // settings value: claude-channels | claude-headless | codex-exec
	Label string   // human label
	Short string   // primary alias for /runtime
	Alias []string // accepted aliases for /runtime
}

// Runtimes lists the access methods. Availability is determined at runtime by
// which adapters the orchestrator has registered (IsRuntimeAvailable).
var Runtimes = []RuntimeOption{
	{ID: "claude-channels", Label: "Claude — channels (persistent session)", Short: "channels", Alias: []string{"channels", "channel"}},
	{ID: "claude-headless", Label: "Claude — SDK / headless", Short: "sdk", Alias: []string{"sdk", "headless", "claude"}},
	{ID: "codex-exec", Label: "ChatGPT — Codex", Short: "codex", Alias: []string{"codex", "chatgpt", "gpt", "openai"}},
}

// ResolveModel maps an alias or full id to a model id. ok is false if unknown.
func ResolveModel(s string) (id string, ok bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "default" || s == "clear" {
		return "", true
	}
	for _, m := range Models {
		if s == m.Alias || s == strings.ToLower(m.ID) {
			return m.ID, true
		}
	}
	return "", false
}

// ModelLabel returns a friendly label for a stored model id.
func ModelLabel(id string) string {
	for _, m := range Models {
		if m.ID == id {
			return m.Label
		}
	}
	if id == "" {
		return "Default"
	}
	return id // custom id
}

// ResolveRuntime maps an alias or id to a runtime id. ok is false if unknown.
func ResolveRuntime(s string) (id string, ok bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	for _, rt := range Runtimes {
		if s == rt.ID {
			return rt.ID, true
		}
		for _, a := range rt.Alias {
			if s == a {
				return rt.ID, true
			}
		}
	}
	return "", false
}

// RuntimeLabel returns a friendly label for a runtime id.
func RuntimeLabel(id string) string {
	for _, rt := range Runtimes {
		if rt.ID == id {
			return rt.Label
		}
	}
	return id
}

// IsRuntimeAvailable reports whether an adapter for the runtime is registered
// (i.e. it can actually run, not just be selected).
func (o *Orchestrator) IsRuntimeAvailable(id string) bool {
	_, ok := o.runtimes[id]
	return ok
}

// RuntimeChoice pairs a catalog entry with its current availability, for the UI.
type RuntimeChoice struct {
	RuntimeOption
	Available bool
}

// RuntimeChoices returns the access methods annotated with availability.
func (o *Orchestrator) RuntimeChoices() []RuntimeChoice {
	out := make([]RuntimeChoice, 0, len(Runtimes))
	for _, rt := range Runtimes {
		out = append(out, RuntimeChoice{RuntimeOption: rt, Available: o.IsRuntimeAvailable(rt.ID)})
	}
	return out
}

// ModelChoices exposes the model catalog to the UI.
func (o *Orchestrator) ModelChoices() []ModelOption { return Models }
