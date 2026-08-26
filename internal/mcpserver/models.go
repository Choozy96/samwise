package mcpserver

import (
	"context"
	"fmt"
	"strings"
)

// ModelResolveFunc validates free-typed model input against the catalog policy:
// a catalog alias/id resolves, a raw "claude-…" id passes through, anything
// else is rejected with (possibly empty) "did you mean" suggestions. Injected
// by the orchestrator, which owns the catalog.
type ModelResolveFunc func(ctx context.Context, input string) (id string, ok bool, suggestions []string)

var modelResolver ModelResolveFunc

// SetModelResolver wires the model-input validator for the agent tools.
func SetModelResolver(fn ModelResolveFunc) { modelResolver = fn }

// resolveModelArg applies the shared policy to a model argument from a tool
// call. Returns the id to store, or a user-facing error message ("" = ok).
// With no resolver wired (tests), input passes through unchanged.
func resolveModelArg(ctx context.Context, input string) (string, string) {
	if modelResolver == nil || strings.TrimSpace(input) == "" {
		return strings.TrimSpace(input), ""
	}
	id, ok, sugg := modelResolver(ctx, input)
	if ok {
		return id, ""
	}
	msg := fmt.Sprintf("no model %q in the catalog", strings.TrimSpace(input))
	if len(sugg) > 0 {
		msg += " — did you mean: " + strings.Join(sugg, ", ") + "?"
	}
	return "", msg + " (full ids starting with claude- are accepted as-is)"
}
