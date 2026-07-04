package mcpserver

import (
	"strings"
	"testing"
)

func TestSkillTools(t *testing.T) {
	h, ctx := newJobHandlers(t)

	r, _, _ := h.skillCreate(ctx, nil, skillCreateIn{Name: "weekly-review", Description: "run a review", Content: "1. gather\n2. summarize"})
	if s := resultText(r); !strings.Contains(s, "Created skill") {
		t.Fatalf("create: %q", s)
	}
	// Persisted to the user (this is what makes it show on the web Skills page).
	if sk, err := h.db.GetSkillByName(ctx, h.userID, "weekly-review"); err != nil || sk == nil || sk.UserID != h.userID || !sk.Enabled {
		t.Fatalf("skill not persisted for user: %+v err=%v", sk, err)
	}
	// Duplicate name is refused.
	r, _, _ = h.skillCreate(ctx, nil, skillCreateIn{Name: "weekly-review", Content: "x"})
	if s := resultText(r); !strings.Contains(s, "already have") {
		t.Errorf("duplicate not rejected: %q", s)
	}
	// List shows it.
	r, _, _ = h.skillList(ctx, nil, emptyIn{})
	if s := resultText(r); !strings.Contains(s, "weekly-review") {
		t.Errorf("list missing skill: %q", s)
	}
	// Disable via update.
	no := false
	h.skillUpdate(ctx, nil, skillUpdateIn{Name: "weekly-review", Enabled: &no})
	if sk, _ := h.db.GetSkillByName(ctx, h.userID, "weekly-review"); sk == nil || sk.Enabled {
		t.Error("skill should be disabled")
	}
	// Delete.
	r, _, _ = h.skillDelete(ctx, nil, skillNameIn{Name: "weekly-review"})
	if s := resultText(r); !strings.Contains(s, "Deleted") {
		t.Errorf("delete: %q", s)
	}
	if _, err := h.db.GetSkillByName(ctx, h.userID, "weekly-review"); err == nil {
		t.Error("skill should be gone after delete")
	}
}

func TestSkillCreateRunnable(t *testing.T) {
	h, ctx := newJobHandlers(t)

	// Create a runnable skill with an entrypoint + a scoped secret.
	r, _, _ := h.skillCreate(ctx, nil, skillCreateIn{
		Name: "bus", Description: "bus lookup", Content: "looks up a bus",
		Entrypoint: "main.py", EnvKeys: "BUS_API_KEY",
	})
	if s := resultText(r); !strings.Contains(s, "runnable") {
		t.Fatalf("create should report runnable: %q", s)
	}
	sk, _ := h.db.GetSkillByName(ctx, h.userID, "bus")
	if sk == nil || sk.Entrypoint != "main.py" || sk.EnvKeys != "BUS_API_KEY" || !sk.Runnable() {
		t.Fatalf("entrypoint/env_keys not persisted: %+v", sk)
	}

	// Clear the entrypoint via update ("-") → no longer runnable.
	h.skillUpdate(ctx, nil, skillUpdateIn{Name: "bus", Entrypoint: "-"})
	if sk, _ := h.db.GetSkillByName(ctx, h.userID, "bus"); sk == nil || sk.Runnable() {
		t.Errorf("entrypoint '-' should clear runnable, got %+v", sk)
	}
}

func TestSelfCustomReadOnlyDenied(t *testing.T) {
	h, ctx := newJobHandlers(t)
	h.readOnly = true

	r1, _, _ := h.skillCreate(ctx, nil, skillCreateIn{Name: "x", Content: "y"})
	r2, _, _ := h.agentCreate(ctx, nil, agentCreateIn{Name: "x", Soul: "y"})
	r3, _, _ := h.agentSwitch(ctx, nil, agentNameIn{Name: "x"})
	r4, _, _ := h.updateSettings(ctx, nil, updateSettingsIn{GroupReply: "all"})
	for name, r := range map[string]string{
		"skill_create":    resultText(r1),
		"agent_create":    resultText(r2),
		"agent_switch":    resultText(r3),
		"update_settings": resultText(r4),
	} {
		if !strings.Contains(r, "Not permitted") {
			t.Errorf("%s should be denied for a read-only run: %q", name, r)
		}
	}
}

func TestAgentTools(t *testing.T) {
	h, ctx := newJobHandlers(t)
	if _, err := h.db.EnsureDefaultAgent(ctx, h.userID); err != nil { // provisioning normally does this
		t.Fatal(err)
	}

	r, _, _ := h.agentCreate(ctx, nil, agentCreateIn{Name: "Coach", Description: "fitness coach", Soul: "You are a tough fitness coach."})
	if s := resultText(r); !strings.Contains(s, "Created agent") {
		t.Fatalf("create: %q", s)
	}
	r, _, _ = h.agentSwitch(ctx, nil, agentNameIn{Name: "Coach"})
	if s := resultText(r); !strings.Contains(s, "Switched") {
		t.Fatalf("switch: %q", s)
	}
	if a, _ := h.db.GetActiveAgent(ctx, h.userID); a == nil || a.Name != "Coach" {
		t.Fatalf("active agent not switched: %+v", a)
	}
	// Edit its own soul (persona self-edit).
	h.agentUpdate(ctx, nil, agentUpdateIn{Name: "Coach", Soul: "You are a gentle, encouraging coach."})
	if a, _ := h.db.GetAgentByName(ctx, h.userID, "Coach"); a == nil || !strings.Contains(a.Soul, "gentle") {
		t.Error("soul not updated")
	}
	// List marks the active one.
	r, _, _ = h.agentList(ctx, nil, emptyIn{})
	if s := resultText(r); !strings.Contains(s, "Coach") || !strings.Contains(s, "active") {
		t.Errorf("list: %q", s)
	}
	// Default can't be deleted.
	def, _ := h.db.GetDefaultAgent(ctx, h.userID)
	r, _, _ = h.agentDelete(ctx, nil, agentNameIn{Name: def.Name})
	if s := resultText(r); !strings.Contains(s, "can't delete the default") {
		t.Errorf("default delete should be refused: %q", s)
	}
	// A non-default agent can.
	r, _, _ = h.agentDelete(ctx, nil, agentNameIn{Name: "Coach"})
	if s := resultText(r); !strings.Contains(s, "Deleted") {
		t.Errorf("delete Coach: %q", s)
	}
}

func TestUpdateSettingsTool(t *testing.T) {
	h, ctx := newJobHandlers(t)
	r, _, _ := h.updateSettings(ctx, nil, updateSettingsIn{DeliveryChannel: "telegram", GroupReply: "all"})
	if s := resultText(r); !strings.Contains(s, "Updated") {
		t.Fatalf("update: %q", s)
	}
	st, _ := h.db.GetSettings(ctx, h.userID)
	if st.DeliveryChannel != "telegram" || st.GroupReplyMode != "all" {
		t.Errorf("settings not applied: delivery=%q group=%q", st.DeliveryChannel, st.GroupReplyMode)
	}
	r, _, _ = h.updateSettings(ctx, nil, updateSettingsIn{DeliveryChannel: "carrier-pigeon"})
	if s := resultText(r); !strings.Contains(s, "must be") {
		t.Errorf("invalid value should fail: %q", s)
	}
}
