package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"samwise/internal/runtime"
	"samwise/internal/store"
)

// Audience tools let the agent control who, in a group chat, may use a given
// skill or built-in tool: 'paired' (registered users only) or 'everyone' (also
// unregistered group senders). Setting them is a write, so it's gated to paired
// users. Exec/write tools (Bash/Write/Edit) are hard-locked to 'paired' UNLESS
// the deployment set ALLOW_EXEC_TOOL_OPENING — see allowExecOpening.

// allowExecOpening mirrors cfg.AllowExecToolOpening; when false (default), the
// write/exec tools can never be opened to everyone. Set once at startup.
var allowExecOpening bool

// SetAllowExecToolOpening installs the deployment's exec-opening policy.
func SetAllowExecToolOpening(v bool) { allowExecOpening = v }

type setSkillAudienceIn struct {
	Name     string `json:"name" jsonschema:"name of one of your own skills"`
	Audience string `json:"audience" jsonschema:"'paired' (registered users only) or 'everyone' (also unregistered group members)"`
}

type setToolAudienceIn struct {
	Tool     string `json:"tool" jsonschema:"built-in tool name, e.g. 'WebSearch', 'WebFetch', 'Read'. Bash/Write/Edit can't be changed."`
	Audience string `json:"audience" jsonschema:"'paired' (registered users only) or 'everyone' (also unregistered group members)"`
}

func (h *handlers) registerAudience(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "set_skill_audience",
		Description: "Set who in a group chat may use one of your skills: 'paired' (only registered/paired users) or 'everyone' (also unregistered group members). Default is 'paired'.",
	}, h.setSkillAudience)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "set_tool_audience",
		Description: "Set who in a group chat may use a built-in tool (e.g. WebSearch, WebFetch): 'paired' or 'everyone'. The write/exec tools Bash, Write, and Edit are always restricted to paired users and can't be opened.",
	}, h.setToolAudience)
}

func normalizeAudienceArg(a string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(a)) {
	case store.AudienceEveryone:
		return store.AudienceEveryone, true
	case store.AudiencePaired:
		return store.AudiencePaired, true
	default:
		return "", false
	}
}

func (h *handlers) setSkillAudience(ctx context.Context, _ *mcp.CallToolRequest, in setSkillAudienceIn) (*mcp.CallToolResult, any, error) {
	if h.readOnly {
		return h.denyWrite("set_skill_audience"), nil, nil
	}
	aud, ok := normalizeAudienceArg(in.Audience)
	if !ok {
		return h.fail("set_skill_audience", in.Audience, "audience must be 'paired' or 'everyone'"), nil, nil
	}
	name := strings.TrimSpace(in.Name)
	sk, err := h.db.GetSkillByName(ctx, h.userID, name)
	if err != nil || sk == nil || sk.IsGlobal() {
		return h.fail("set_skill_audience", name, "no such skill you own"), nil, nil
	}
	sk.Audience = aud
	if err := h.db.UpdateSkill(ctx, *sk); err != nil {
		return h.fail("set_skill_audience", name, err.Error()), nil, nil
	}
	h.audit("set_skill_audience", "name="+name+" audience="+aud, "ok")
	who := "registered/paired users only"
	if aud == store.AudienceEveryone {
		who = "everyone in the group (incl. unregistered members)"
	}
	return textResult(fmt.Sprintf("Skill %q is now usable by %s.", name, who)), nil, nil
}

func (h *handlers) setToolAudience(ctx context.Context, _ *mcp.CallToolRequest, in setToolAudienceIn) (*mcp.CallToolResult, any, error) {
	if h.readOnly {
		return h.denyWrite("set_tool_audience"), nil, nil
	}
	aud, ok := normalizeAudienceArg(in.Audience)
	if !ok {
		return h.fail("set_tool_audience", in.Audience, "audience must be 'paired' or 'everyone'"), nil, nil
	}
	tool := strings.TrimSpace(in.Tool)
	if !runtime.IsKnownBuiltinTool(tool) {
		return h.fail("set_tool_audience", tool, "unknown tool — use a built-in tool name like 'WebSearch', 'WebFetch', or 'Read'"), nil, nil
	}
	if !runtime.AudienceConfigurable(tool) && !allowExecOpening {
		return h.fail("set_tool_audience", tool, "Bash, Write, and Edit are restricted to paired users (a stranger must never run code or write files as you) and can't be opened to everyone on this deployment"), nil, nil
	}
	s, err := h.db.GetSettings(ctx, h.userID)
	if err != nil {
		return h.fail("set_tool_audience", tool, err.Error()), nil, nil
	}
	m := store.ParseToolAudience(s.ToolAudience)
	if aud == store.AudienceEveryone {
		m[tool] = store.AudienceEveryone
	} else {
		delete(m, tool) // back to default ('paired' for everything configurable here)
	}
	s.ToolAudience = store.MarshalToolAudience(m)
	if err := h.db.UpdateSettings(ctx, s); err != nil {
		return h.fail("set_tool_audience", tool, err.Error()), nil, nil
	}
	h.audit("set_tool_audience", "tool="+tool+" audience="+aud, "ok")
	who := "registered/paired users only"
	if aud == store.AudienceEveryone {
		who = "everyone in the group (incl. unregistered members)"
	}
	msg := fmt.Sprintf("Tool %q is now available to %s. (It still has to be enabled in settings to be usable at all.)", tool, who)
	// Opening a write/exec tool to everyone is dangerous — say so plainly.
	if aud == store.AudienceEveryone && runtime.IsExecTool(tool) {
		msg = fmt.Sprintf("⚠️ WARNING: %q is now open to EVERYONE in the group. Any unregistered member can now run code / write files AS the owner, with the owner's secrets in the environment — they could read your API tokens or delete your files. Only keep this if you fully trust everyone in the chat. Tool %q → %s.", tool, tool, who)
	}
	return textResult(msg), nil, nil
}
