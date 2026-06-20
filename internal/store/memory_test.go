package store

import (
	"context"
	"path/filepath"
	"testing"
)

// newTestDB opens a fresh migrated database in a temp dir.
func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestSearchMemoryUserScopedFTS guards the FTS user_id binding: the filter must
// match the stored integer user_id, and must isolate users from each other.
func TestSearchMemoryUserScopedFTS(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	uA, err := db.CreateUser(ctx, "alice", "h", true)
	if err != nil {
		t.Fatal(err)
	}
	uB, err := db.CreateUser(ctx, "bob", "h", false)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.SaveSemantic(ctx, uA, 0, "health", "fact", "Allergic to peanuts", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveSemantic(ctx, uB, 0, "health", "fact", "Allergic to shellfish", "test"); err != nil {
		t.Fatal(err)
	}

	// Alice's query must find her own row (stemmed match on "allergic").
	hits, err := db.SearchMemory(ctx, uA, AllAgents, "am I allergic to anything?", "", "", "", 8)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit for alice, got %d: %+v", len(hits), hits)
	}
	if hits[0].Content != "Allergic to peanuts" {
		t.Errorf("alice got %q, want peanuts", hits[0].Content)
	}

	// Cross-user isolation: alice must never see bob's memory.
	for _, h := range hits {
		if h.Content == "Allergic to shellfish" {
			t.Fatal("cross-user leak: alice saw bob's memory")
		}
	}

	// Topic filter.
	none, err := db.SearchMemory(ctx, uA, AllAgents, "allergic", "work", "", "", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("topic filter failed: expected 0, got %d", len(none))
	}
}

// TestMemoryAgentScope is the core of the user/agent memory split: a run as agent
// A sees user-scoped memory plus its own, but never another agent's; user-scope
// (0) sees only shared memory; AllAgents (the editor) sees everything.
func TestMemoryAgentScope(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	uid, _ := db.CreateUser(ctx, "alice", "h", false)
	const agentA, agentB = int64(10), int64(20)

	if _, err := db.SaveSemantic(ctx, uid, 0, "t", "fact", "shared user fact", "test"); err != nil {
		t.Fatal(err)
	}
	db.SaveSemantic(ctx, uid, agentA, "t", "fact", "agent A persona fact", "test")
	db.SaveSemantic(ctx, uid, agentB, "t", "fact", "agent B persona fact", "test")

	collect := func(scope int64) map[string]bool {
		hits, err := db.SearchMemory(ctx, uid, scope, "fact", "", "", "", 10)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, h := range hits {
			got[h.Content] = true
		}
		return got
	}

	a := collect(agentA)
	if !a["shared user fact"] || !a["agent A persona fact"] {
		t.Error("agent A should see user memory and its own")
	}
	if a["agent B persona fact"] {
		t.Error("LEAK: agent A saw agent B's memory")
	}

	u := collect(0)
	if !u["shared user fact"] || u["agent A persona fact"] || u["agent B persona fact"] {
		t.Errorf("user-scope (0) should see only shared memory, got %v", u)
	}

	if all := collect(AllAgents); len(all) != 3 {
		t.Errorf("AllAgents should see all three scopes, got %v", all)
	}
}
