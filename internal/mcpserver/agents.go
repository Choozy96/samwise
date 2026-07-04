package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"samwise/internal/store"
)

// Agent tools let the agent manage its own personas: create new ones, switch the
// active one, edit a persona's soul (including its own), and delete non-default
// ones. Writes are gated to registered (paired) users and scoped to the run's
// user. Switching sets the user's active agent (new messages use it); a Telegram
// bot bound to a specific agent still uses its binding for that bot.

type agentCreateIn struct {
	Name        string `json:"name" jsonschema:"short unique name for the persona, e.g. 'Coach' or 'Work'"`
	Description string `json:"description" jsonschema:"one line on this persona's purpose"`
	Soul        string `json:"soul" jsonschema:"the persona's system prompt — its personality, role, and standing instructions"`
	Model       string `json:"model,omitempty" jsonschema:"optional model override for this persona; omit to inherit the user's setting"`
	Runtime     string `json:"runtime,omitempty" jsonschema:"optional runtime/access-method override; omit to inherit the user's setting"`
}

type agentUpdateIn struct {
	Name        string `json:"name" jsonschema:"current name of the agent to change"`
	NewName     string `json:"new_name,omitempty" jsonschema:"rename the agent to this"`
	Description string `json:"description,omitempty" jsonschema:"new description"`
	Soul        string `json:"soul,omitempty" jsonschema:"new system prompt / persona instructions — this is how you edit a personality (use the active agent's name to edit your own)"`
	Model       string `json:"model,omitempty" jsonschema:"new model override; '-' clears it (inherit the user's setting)"`
	Runtime     string `json:"runtime,omitempty" jsonschema:"new runtime override; '-' clears it"`
	Enabled     *bool  `json:"enabled,omitempty" jsonschema:"enable or disable the agent"`
}

type agentNameIn struct {
	Name string `json:"name" jsonschema:"the agent's name"`
}

func (h *handlers) registerAgents(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "agent_create",
		Description: "Create a new agent persona (a named personality/role with its own system prompt). It appears on the web Agents page; switch to it with agent_switch.",
	}, h.agentCreate)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "agent_list",
		Description: "List the user's agent personas, marking the default and the currently active one.",
	}, h.agentList)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "agent_switch",
		Description: "Switch the active agent persona by name. New messages use it. (A Telegram bot bound to a specific agent still uses that binding for that bot.)",
	}, h.agentSwitch)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "agent_update",
		Description: "Change an agent by name: rename, or update its description, soul (system prompt / personality), model, runtime, or enabled state. To refine your OWN personality, update the active agent's soul. Only the fields you provide change.",
	}, h.agentUpdate)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "agent_delete",
		Description: "Delete an agent persona by name. The default agent can't be deleted.",
	}, h.agentDelete)
}

func (h *handlers) agentCreate(ctx context.Context, _ *mcp.CallToolRequest, in agentCreateIn) (*mcp.CallToolResult, any, error) {
	if h.readOnly {
		return h.denyWrite("agent_create"), nil, nil
	}
	name := strings.TrimSpace(in.Name)
	soul := strings.TrimSpace(in.Soul)
	if name == "" || soul == "" {
		return h.fail("agent_create", name, "name and soul are required"), nil, nil
	}
	if ex, err := h.db.GetAgentByName(ctx, h.userID, name); err == nil && ex != nil {
		return h.fail("agent_create", name, "you already have an agent with that name — use agent_update to change it"), nil, nil
	}
	id, err := h.db.CreateAgent(ctx, store.Agent{
		UserID: h.userID, Name: name, Description: strings.TrimSpace(in.Description), Soul: soul,
		Model: strings.TrimSpace(in.Model), Runtime: strings.TrimSpace(in.Runtime), Enabled: true,
	})
	if err != nil {
		return h.fail("agent_create", name, err.Error()), nil, nil
	}
	h.audit("agent_create", "name="+name, "ok")
	return textResult(fmt.Sprintf("Created agent %q (id=%d). Switch to it with agent_switch, or the user can pick it on the Agents page.", name, id)), nil, nil
}

func (h *handlers) agentList(ctx context.Context, _ *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, any, error) {
	agents, err := h.db.ListAgents(ctx, h.userID)
	if err != nil {
		return h.fail("agent_list", "", err.Error()), nil, nil
	}
	if len(agents) == 0 {
		return textResult("No agents yet."), nil, nil
	}
	active, _ := h.db.GetActiveAgent(ctx, h.userID)
	var b strings.Builder
	for _, a := range agents {
		tag := ""
		if a.IsDefault {
			tag += " (default)"
		}
		if active != nil && a.ID == active.ID {
			tag += " ← active"
		}
		if !a.Enabled {
			tag += " [disabled]"
		}
		fmt.Fprintf(&b, "- %s — %s%s\n", a.Name, a.Description, tag)
	}
	h.audit("agent_list", fmt.Sprintf("n=%d", len(agents)), "ok")
	return textResult(strings.TrimRight(b.String(), "\n")), nil, nil
}

func (h *handlers) agentSwitch(ctx context.Context, _ *mcp.CallToolRequest, in agentNameIn) (*mcp.CallToolResult, any, error) {
	if h.readOnly {
		return h.denyWrite("agent_switch"), nil, nil
	}
	name := strings.TrimSpace(in.Name)
	a, err := h.db.GetAgentByName(ctx, h.userID, name)
	if err != nil || a == nil {
		return h.fail("agent_switch", name, "no such agent"), nil, nil
	}
	if !a.Enabled {
		return h.fail("agent_switch", name, "that agent is disabled — enable it first with agent_update"), nil, nil
	}
	if err := h.db.SetActiveAgent(ctx, h.userID, a.ID); err != nil {
		return h.fail("agent_switch", name, err.Error()), nil, nil
	}
	h.audit("agent_switch", "name="+a.Name, "ok")
	return textResult(fmt.Sprintf("Switched the active agent to %q. New messages use it.", a.Name)), nil, nil
}

func (h *handlers) agentUpdate(ctx context.Context, _ *mcp.CallToolRequest, in agentUpdateIn) (*mcp.CallToolResult, any, error) {
	if h.readOnly {
		return h.denyWrite("agent_update"), nil, nil
	}
	name := strings.TrimSpace(in.Name)
	a, err := h.db.GetAgentByName(ctx, h.userID, name)
	if err != nil || a == nil {
		return h.fail("agent_update", name, "no such agent"), nil, nil
	}
	if s := strings.TrimSpace(in.NewName); s != "" {
		a.Name = s
	}
	if s := strings.TrimSpace(in.Description); s != "" {
		a.Description = s
	}
	if s := strings.TrimSpace(in.Soul); s != "" {
		a.Soul = s
	}
	if s := strings.TrimSpace(in.Model); s != "" {
		a.Model = clearable(s)
	}
	if s := strings.TrimSpace(in.Runtime); s != "" {
		a.Runtime = clearable(s)
	}
	if in.Enabled != nil {
		a.Enabled = *in.Enabled
	}
	if err := h.db.UpdateAgent(ctx, *a); err != nil {
		return h.fail("agent_update", name, err.Error()), nil, nil
	}
	h.audit("agent_update", "name="+name, "ok")
	return textResult(fmt.Sprintf("Updated agent %q.", a.Name)), nil, nil
}

func (h *handlers) agentDelete(ctx context.Context, _ *mcp.CallToolRequest, in agentNameIn) (*mcp.CallToolResult, any, error) {
	if h.readOnly {
		return h.denyWrite("agent_delete"), nil, nil
	}
	name := strings.TrimSpace(in.Name)
	a, err := h.db.GetAgentByName(ctx, h.userID, name)
	if err != nil || a == nil {
		return h.fail("agent_delete", name, "no such agent"), nil, nil
	}
	if a.IsDefault {
		return h.fail("agent_delete", name, "can't delete the default agent — make another agent the default first (on the web Agents page)"), nil, nil
	}
	if err := h.db.DeleteAgent(ctx, h.userID, a.ID); err != nil {
		return h.fail("agent_delete", name, err.Error()), nil, nil
	}
	h.audit("agent_delete", "name="+name, "ok")
	return textResult(fmt.Sprintf("Deleted agent %q.", name)), nil, nil
}
