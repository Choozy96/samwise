package orchestrator

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"samwise/internal/store"
)

func newCatalogOrch(t *testing.T) (*Orchestrator, context.Context) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return &Orchestrator{db: db}, context.Background()
}

// TestModelCatalogSeeded: migration 0028 seeds the catalog, including the new
// Claude 5 models, and ModelChoices returns them.
func TestModelCatalogSeeded(t *testing.T) {
	o, ctx := newCatalogOrch(t)
	choices := o.ModelChoices(ctx)
	if len(choices) < 4 {
		t.Fatalf("expected seeded catalog, got %d", len(choices))
	}
	haveFable := false
	for _, m := range choices {
		if m.ID == "claude-fable-5" {
			haveFable = true
		}
	}
	if !haveFable {
		t.Error("Fable 5 should be seeded in the catalog")
	}
}

// TestResolveModelDBBacked: aliases and ids resolve from the DB catalog;
// default/clear map to the runtime default; unknown returns ok=false.
func TestResolveModelDBBacked(t *testing.T) {
	o, ctx := newCatalogOrch(t)

	if id, ok := o.ResolveModel(ctx, "fable5"); !ok || id != "claude-fable-5" {
		t.Errorf("alias fable5 → (%q,%v)", id, ok)
	}
	if id, ok := o.ResolveModel(ctx, "CLAUDE-OPUS-4-8"); !ok || id != "claude-opus-4-8" {
		t.Errorf("id resolve → (%q,%v)", id, ok)
	}
	if id, ok := o.ResolveModel(ctx, "default"); !ok || id != "" {
		t.Errorf("default → (%q,%v)", id, ok)
	}
	if _, ok := o.ResolveModel(ctx, "gpt-9"); ok {
		t.Error("unknown model should not resolve")
	}
}

// TestAdminModelCRUD: a newly added model becomes resolvable + labeled; a
// disabled model is hidden from choices/resolve but keeps its label.
func TestAdminModelCRUD(t *testing.T) {
	o, ctx := newCatalogOrch(t)

	id, err := o.db.CreateModel(ctx, store.Model{Alias: "opus5", ModelID: "claude-opus-5", Label: "Claude Opus 5", Sort: 5, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := o.ResolveModel(ctx, "opus5"); !ok || got != "claude-opus-5" {
		t.Errorf("new model not resolvable: (%q,%v)", got, ok)
	}
	if lbl := o.ModelLabel(ctx, "claude-opus-5"); lbl != "Claude Opus 5" {
		t.Errorf("label = %q", lbl)
	}

	// Disable it: gone from resolve/choices, but the label survives.
	m, _ := o.db.GetModel(ctx, id)
	m.Enabled = false
	if err := o.db.UpdateModel(ctx, *m); err != nil {
		t.Fatal(err)
	}
	if _, ok := o.ResolveModel(ctx, "opus5"); ok {
		t.Error("disabled model should not resolve")
	}
	if lbl := o.ModelLabel(ctx, "claude-opus-5"); lbl != "Claude Opus 5" {
		t.Errorf("disabled model should keep its label, got %q", lbl)
	}
}

// TestCmdModelSetsAgent: /model changes the ACTIVE AGENT's model, not settings.
func TestCmdModelSetsAgent(t *testing.T) {
	o, ctx := newCatalogOrch(t)
	uid, err := o.db.CreateUser(ctx, "alice", "h", true)
	if err != nil {
		t.Fatal(err)
	}
	aid, err := o.db.CreateAgent(ctx, store.Agent{UserID: uid, Name: "Assistant", Enabled: true, IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := o.db.SetActiveAgent(ctx, uid, aid); err != nil {
		t.Fatal(err)
	}

	out := o.cmdModel(ctx, uid, "fable5")
	if !strings.Contains(out, "Fable 5") {
		t.Fatalf("cmdModel reply: %q", out)
	}
	a, _ := o.db.GetActiveAgent(ctx, uid)
	if a.Model != "claude-fable-5" {
		t.Errorf("agent model = %q, want claude-fable-5", a.Model)
	}
	s, _ := o.db.GetSettings(ctx, uid)
	if strings.Contains(s.ModelHints, "fable") {
		t.Errorf("settings hints must not change: %q", s.ModelHints)
	}
	// "default" clears the override.
	_ = o.cmdModel(ctx, uid, "default")
	if a, _ := o.db.GetActiveAgent(ctx, uid); a.Model != "" {
		t.Errorf("default should clear the model, got %q", a.Model)
	}
}

// TestMigrationCopiesHintToAgents: 0029 moves a pre-existing settings-level chat
// model into agents that had no override. (Simulated by setting the hint, then
// re-running the copy statement semantics via a fresh migrate on a seeded DB is
// not possible — instead verify agentModel no longer reads settings.)
func TestAgentModelIgnoresSettings(t *testing.T) {
	a := &store.Agent{Model: ""}
	s := &store.Settings{ModelHints: `{"chat":"claude-opus-4-8"}`}
	if got := agentModel(a, s); got != "" {
		t.Errorf("agentModel must ignore settings hints now, got %q", got)
	}
	a.Model = "claude-fable-5"
	if got := agentModel(a, s); got != "claude-fable-5" {
		t.Errorf("agent override should win: %q", got)
	}
}

// TestResolveModelInput covers the free-typed input policy: catalog entries
// resolve, plausible raw claude- ids pass through, garbage is rejected.
func TestResolveModelInput(t *testing.T) {
	o, ctx := newCatalogOrch(t)
	if id, ok := o.ResolveModelInput(ctx, "fable5"); !ok || id != "claude-fable-5" {
		t.Errorf("alias: (%q,%v)", id, ok)
	}
	if id, ok := o.ResolveModelInput(ctx, "claude-opus-6"); !ok || id != "claude-opus-6" {
		t.Errorf("raw claude- id should pass through: (%q,%v)", id, ok)
	}
	if _, ok := o.ResolveModelInput(ctx, "opsu5"); ok {
		t.Error("typo should be rejected, not stored")
	}
	if _, ok := o.ResolveModelInput(ctx, "gpt-5"); ok {
		t.Error("non-claude unknown should be rejected")
	}
}

// TestSuggestModels: a typo suggests the intended model; total garbage suggests
// nothing.
func TestSuggestModels(t *testing.T) {
	o, ctx := newCatalogOrch(t)
	sugg := o.SuggestModels(ctx, "opsu48", 3)
	if len(sugg) == 0 || sugg[0].Alias != "opus48" {
		t.Errorf("opsu48 should suggest opus48 first, got %+v", sugg)
	}
	sugg = o.SuggestModels(ctx, "fable", 3)
	if len(sugg) == 0 || sugg[0].Alias != "fable5" {
		t.Errorf("fable should suggest fable5 first, got %+v", sugg)
	}
	if sugg := o.SuggestModels(ctx, "zzzzqqqq", 3); len(sugg) != 0 {
		t.Errorf("garbage should suggest nothing, got %+v", sugg)
	}
}

// TestCmdModelRejectsUnknown: /model with a typo doesn't change the agent and
// replies with a did-you-mean.
func TestCmdModelRejectsUnknown(t *testing.T) {
	o, ctx := newCatalogOrch(t)
	uid, err := o.db.CreateUser(ctx, "alice", "h", true) // seeds a default "Assistant" agent
	if err != nil {
		t.Fatal(err)
	}

	reply := o.cmdModel(ctx, uid, "opsu48")
	if !strings.Contains(reply, "No model") || !strings.Contains(reply, "opus48") {
		t.Errorf("expected rejection with suggestion, got: %q", reply)
	}
	a, _ := o.db.GetAgentByName(ctx, uid, "Assistant")
	if a.Model != "" {
		t.Errorf("agent model should be unchanged, got %q", a.Model)
	}

	// A raw claude- id is still accepted (forward compat).
	reply = o.cmdModel(ctx, uid, "claude-opus-6")
	if !strings.Contains(reply, "set to") {
		t.Errorf("raw id should be accepted: %q", reply)
	}
	a, _ = o.db.GetAgentByName(ctx, uid, "Assistant")
	if a.Model != "claude-opus-6" {
		t.Errorf("agent model = %q, want claude-opus-6", a.Model)
	}
}
