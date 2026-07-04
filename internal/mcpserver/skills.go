package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"samwise/internal/store"
)

// Skill tools let the agent manage its own markdown "skills" (reusable
// playbooks) — the same ones shown on the web Skills page. Writes are gated to
// registered (paired) users via readOnly and scoped to the run's user, so an
// agent can only touch its own user's skills (global skills are read-only here).

type skillCreateIn struct {
	Name        string `json:"name" jsonschema:"short unique skill name, e.g. 'weekly-review'"`
	Description string `json:"description" jsonschema:"one line on when to use this skill"`
	Content     string `json:"content" jsonschema:"the skill body in markdown — the playbook/instructions to follow"`
	AlwaysOn    bool   `json:"always_on,omitempty" jsonschema:"true = loaded into every run; false (default) = loaded when relevant or asked for by name"`
	Entrypoint  string `json:"entrypoint,omitempty" jsonschema:"optional: a script file (relative to the skill's bundle folder) to make the skill runnable via skill_run, e.g. 'main.py'. Write the actual script into your workspace at skills/<name>/ with your file tools."`
	EnvKeys     string `json:"env_keys,omitempty" jsonschema:"optional, comma-separated: the secret names this skill's script may receive as env vars (only these are injected, never your other secrets)"`
}

type skillUpdateIn struct {
	Name        string `json:"name" jsonschema:"name of the skill to change"`
	Description string `json:"description,omitempty" jsonschema:"new description"`
	Content     string `json:"content,omitempty" jsonschema:"new markdown body"`
	Enabled     *bool  `json:"enabled,omitempty" jsonschema:"enable (true) or disable (false)"`
	AlwaysOn    *bool  `json:"always_on,omitempty" jsonschema:"set whether it loads into every run"`
	Entrypoint  string `json:"entrypoint,omitempty" jsonschema:"set the runnable script (relative to skills/<name>/), or '-' to make the skill not runnable again"`
	EnvKeys     string `json:"env_keys,omitempty" jsonschema:"comma-separated secret names the script may use, or '-' to clear"`
}

type skillNameIn struct {
	Name string `json:"name" jsonschema:"name of one of your own skills"`
}

func (h *handlers) registerSkills(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "skill_create",
		Description: "Create a new skill — a reusable markdown playbook (also shown on the web Skills page). To make a skill that RUNS a script: set 'entrypoint' (e.g. 'main.py'), write that script into your workspace at skills/<name>/ with your file tools (and a requirements.txt + venv if it needs Python packages), then run it with skill_run.",
	}, h.skillCreate)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "skill_list",
		Description: "List the skills available to you (your own plus any global ones) with their enabled / always-on state.",
	}, h.skillList)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "skill_update",
		Description: "Change one of your skills by name: its description, content, enabled, or always-on state. Only the fields you provide change.",
	}, h.skillUpdate)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "skill_delete",
		Description: "Delete one of your own skills by name.",
	}, h.skillDelete)
}

func (h *handlers) skillCreate(ctx context.Context, _ *mcp.CallToolRequest, in skillCreateIn) (*mcp.CallToolResult, any, error) {
	if h.readOnly {
		return h.denyWrite("skill_create"), nil, nil
	}
	name := strings.TrimSpace(in.Name)
	content := strings.TrimSpace(in.Content)
	if name == "" || content == "" {
		return h.fail("skill_create", name, "name and content are required"), nil, nil
	}
	if ex, err := h.db.GetSkillByName(ctx, h.userID, name); err == nil && ex != nil && ex.UserID == h.userID {
		return h.fail("skill_create", name, "you already have a skill with that name — use skill_update to change it"), nil, nil
	}
	id, err := h.db.CreateSkill(ctx, store.Skill{
		UserID: h.userID, Name: name, Description: strings.TrimSpace(in.Description),
		Content: content, AlwaysOn: in.AlwaysOn, Enabled: true,
		Entrypoint: strings.TrimSpace(in.Entrypoint), EnvKeys: strings.TrimSpace(in.EnvKeys),
	})
	if err != nil {
		return h.fail("skill_create", name, err.Error()), nil, nil
	}
	h.audit("skill_create", "name="+name, "ok")
	msg := fmt.Sprintf("Created skill %q (id=%d) — enabled, and now visible on the Skills page.", name, id)
	if strings.TrimSpace(in.Entrypoint) != "" {
		msg += fmt.Sprintf(" It's runnable: write the script to skills/%s/%s in your workspace, then call skill_run.", name, strings.TrimSpace(in.Entrypoint))
	}
	return textResult(msg), nil, nil
}

func (h *handlers) skillList(ctx context.Context, _ *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, any, error) {
	skills, err := h.db.ListSkillsForUser(ctx, h.userID)
	if err != nil {
		return h.fail("skill_list", "", err.Error()), nil, nil
	}
	if len(skills) == 0 {
		return textResult("No skills yet. Create one with skill_create."), nil, nil
	}
	var b strings.Builder
	for _, sk := range skills {
		tags := ""
		if !sk.Enabled {
			tags += " [disabled]"
		}
		if sk.AlwaysOn {
			tags += " [always-on]"
		}
		if sk.IsGlobal() {
			tags += " [global]"
		}
		if sk.OpenToEveryone() {
			tags += " [everyone]"
		}
		if sk.Runnable() {
			tags += " [runnable:" + sk.Entrypoint + "]"
		}
		fmt.Fprintf(&b, "- %s — %s%s\n", sk.Name, sk.Description, tags)
	}
	h.audit("skill_list", fmt.Sprintf("n=%d", len(skills)), "ok")
	return textResult(strings.TrimRight(b.String(), "\n")), nil, nil
}

func (h *handlers) skillUpdate(ctx context.Context, _ *mcp.CallToolRequest, in skillUpdateIn) (*mcp.CallToolResult, any, error) {
	if h.readOnly {
		return h.denyWrite("skill_update"), nil, nil
	}
	name := strings.TrimSpace(in.Name)
	sk, err := h.db.GetSkillByName(ctx, h.userID, name)
	if err != nil || sk == nil {
		return h.fail("skill_update", name, "no such skill"), nil, nil
	}
	if sk.IsGlobal() {
		return h.fail("skill_update", name, "that's a global (admin-managed) skill; you can't change it"), nil, nil
	}
	if s := strings.TrimSpace(in.Description); s != "" {
		sk.Description = s
	}
	if s := strings.TrimSpace(in.Content); s != "" {
		sk.Content = s
	}
	if in.Enabled != nil {
		sk.Enabled = *in.Enabled
	}
	if in.AlwaysOn != nil {
		sk.AlwaysOn = *in.AlwaysOn
	}
	if s := strings.TrimSpace(in.Entrypoint); s != "" {
		sk.Entrypoint = clearable(s) // "-" clears (skill becomes non-runnable)
	}
	if s := strings.TrimSpace(in.EnvKeys); s != "" {
		sk.EnvKeys = clearable(s)
	}
	if err := h.db.UpdateSkill(ctx, *sk); err != nil {
		return h.fail("skill_update", name, err.Error()), nil, nil
	}
	h.audit("skill_update", "name="+name, "ok")
	return textResult(fmt.Sprintf("Updated skill %q.", sk.Name)), nil, nil
}

func (h *handlers) skillDelete(ctx context.Context, _ *mcp.CallToolRequest, in skillNameIn) (*mcp.CallToolResult, any, error) {
	if h.readOnly {
		return h.denyWrite("skill_delete"), nil, nil
	}
	name := strings.TrimSpace(in.Name)
	sk, err := h.db.GetSkillByName(ctx, h.userID, name)
	if err != nil || sk == nil || sk.IsGlobal() {
		return h.fail("skill_delete", name, "no such skill you own"), nil, nil
	}
	if err := h.db.DeleteSkill(ctx, h.userID, sk.ID); err != nil {
		return h.fail("skill_delete", name, err.Error()), nil, nil
	}
	h.audit("skill_delete", "name="+name, "ok")
	return textResult(fmt.Sprintf("Deleted skill %q.", name)), nil, nil
}
