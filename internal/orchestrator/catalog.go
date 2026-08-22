package orchestrator

import (
	"context"
	"sort"
	"strings"
)

// This file defines the catalogs of selectable models and access methods
// (runtimes), shared by the settings UI and the slash-command handler so both
// agree on names, aliases, and labels.

// ModelOption is a selectable chat model.
type ModelOption struct {
	Alias string // short name for /model and form values: "", opus, sonnet, haiku
	ID    string // the model id passed to the runtime ("" = runtime default)
	Label string // human label for the UI
}

// builtinModels is the fallback model list, used only if the DB catalog can't be
// read (it's seeded from this set by migration 0028). The live catalog is
// admin-editable in the DB — see ModelChoices / ResolveModel / ModelLabel.
// Aliases carry the version so /model stays unambiguous across releases. "Use
// the runtime's default" is NOT a catalog entry — pickers render it statically
// and ResolveModel special-cases it, so an admin can't break it.
var builtinModels = []ModelOption{
	{Alias: "opus48", ID: "claude-opus-4-8", Label: "Claude Opus 4.8"},
	{Alias: "sonnet5", ID: "claude-sonnet-5", Label: "Claude Sonnet 5"},
	{Alias: "fable5", ID: "claude-fable-5", Label: "Claude Fable 5"},
	{Alias: "haiku45", ID: "claude-haiku-4-5-20251001", Label: "Claude Haiku 4.5"},
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

// catalogModels reads the admin-configurable model catalog from the DB, falling
// back to the built-in list if the read fails or the table is somehow empty.
// enabledOnly hides disabled rows (for the user-facing pickers/resolver).
func (o *Orchestrator) catalogModels(ctx context.Context, enabledOnly bool) []ModelOption {
	rows, err := o.db.ListModels(ctx, enabledOnly)
	if err != nil || len(rows) == 0 {
		return builtinModels
	}
	out := make([]ModelOption, 0, len(rows))
	for _, m := range rows {
		out = append(out, ModelOption{Alias: m.Alias, ID: m.ModelID, Label: m.Label})
	}
	return out
}

// ModelChoices exposes the enabled model catalog to the UI.
func (o *Orchestrator) ModelChoices(ctx context.Context) []ModelOption {
	return o.catalogModels(ctx, true)
}

// ResolveModel maps an alias or full id (from the enabled catalog) to a model
// id. ok is false if unknown — callers may still accept a raw id for forward
// compatibility. "default"/"clear"/"" always resolve to the runtime's default.
func (o *Orchestrator) ResolveModel(ctx context.Context, s string) (id string, ok bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "default" || s == "clear" {
		return "", true
	}
	for _, m := range o.catalogModels(ctx, true) {
		if s == strings.ToLower(m.Alias) || s == strings.ToLower(m.ID) {
			return m.ID, true
		}
	}
	return "", false
}

// ResolveModelInput applies the shared policy for free-typed model input
// (/model, the Agents form, the agent_create/update tools):
//   - a catalog alias or id resolves normally;
//   - an unlisted id that still LOOKS like a real model id ("claude-…") is
//     accepted raw, so brand-new models work before they're cataloged;
//   - anything else is rejected (ok=false) instead of being silently stored as
//     a broken raw id — callers should show SuggestModels hints.
func (o *Orchestrator) ResolveModelInput(ctx context.Context, input string) (id string, ok bool) {
	if id, ok = o.ResolveModel(ctx, input); ok {
		return id, true
	}
	raw := strings.TrimSpace(input)
	if strings.HasPrefix(strings.ToLower(raw), "claude-") {
		return raw, true
	}
	return "", false
}

// SuggestModels returns up to n catalog entries closest to an unrecognized
// input (best first) for "did you mean" hints. Only plausible matches are
// returned — possibly none.
func (o *Orchestrator) SuggestModels(ctx context.Context, input string, n int) []ModelOption {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return nil
	}
	type scored struct {
		m ModelOption
		d int
	}
	var cands []scored
	for _, m := range o.catalogModels(ctx, true) {
		best := 1 << 30
		for _, c := range []string{strings.ToLower(m.Alias), strings.ToLower(m.ID), strings.ToLower(m.Label)} {
			if c == "" {
				continue
			}
			d := levenshtein(input, c)
			// Containment either way is a strong signal even when lengths differ
			// a lot (e.g. "opus" vs "claude-opus-5").
			if strings.Contains(c, input) || strings.Contains(input, c) {
				d = min(d, 1)
			}
			best = min(best, d)
		}
		cands = append(cands, scored{m, best})
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].d < cands[j].d })
	var out []ModelOption
	for _, c := range cands {
		if c.d > 3 || len(out) >= n {
			break
		}
		out = append(out, c.m)
	}
	return out
}

// levenshtein is a small edit-distance for the fuzzy model suggestions.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(min(cur[j-1]+1, prev[j]+1), prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// ModelLabel returns a friendly label for a stored model id, from the full
// catalog (enabled or not, so a disabled model still shows its name).
func (o *Orchestrator) ModelLabel(ctx context.Context, id string) string {
	for _, m := range o.catalogModels(ctx, false) {
		if m.ID == id && id != "" {
			return m.Label
		}
	}
	if id == "" {
		return "Default"
	}
	return id // a raw/custom id not in the catalog
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
