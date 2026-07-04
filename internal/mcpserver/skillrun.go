package mcpserver

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SkillRunFunc executes a runnable skill's entrypoint and returns its stdout.
// readOnly marks an unregistered group caller (the executor enforces that such a
// caller may only run skills opened to everyone). Provided by the orchestrator.
type SkillRunFunc func(ctx context.Context, userID int64, name, input string, readOnly bool) (string, error)

var skillExecutor SkillRunFunc

// SetSkillExecutor installs the orchestrator's sandboxed skill runner (called
// once at startup). Without it, skill_run reports execution is unavailable.
func SetSkillExecutor(fn SkillRunFunc) { skillExecutor = fn }

type skillRunIn struct {
	Name  string `json:"name" jsonschema:"name of a runnable skill (one with a script entrypoint)"`
	Input string `json:"input,omitempty" jsonschema:"input passed to the skill's script as a single argument (plain data, not a command)"`
}

func (h *handlers) registerSkillRun(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "skill_run",
		Description: "Run a runnable skill's script and return its output. Use for skills that execute code (e.g. an API lookup). Pass the skill name and an optional input string. This runs only the skill's fixed entrypoint in a sandbox with a limited environment — it is NOT a shell and can't run arbitrary commands.",
	}, h.skillRun)
}

func (h *handlers) skillRun(ctx context.Context, _ *mcp.CallToolRequest, in skillRunIn) (*mcp.CallToolResult, any, error) {
	if skillExecutor == nil {
		return h.fail("skill_run", in.Name, "skill execution isn't available on this deployment"), nil, nil
	}
	name := strings.TrimSpace(in.Name)
	// No denyWrite here: a read-only (unregistered) caller IS allowed to run a
	// skill the owner opened to everyone — the executor enforces that gate, plus
	// the sandbox (fixed entrypoint, scoped env, timeout).
	out, err := skillExecutor(ctx, h.userID, name, in.Input, h.readOnly)
	if err != nil {
		h.audit("skill_run", "name="+name, "fail")
		return h.fail("skill_run", name, err.Error()), nil, nil
	}
	if strings.TrimSpace(out) == "" {
		out = "(the skill ran and produced no output)"
	}
	return textResult(out), nil, nil
}
