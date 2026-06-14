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

	if _, err := db.SaveSemantic(ctx, uA, "health", "fact", "Allergic to peanuts", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveSemantic(ctx, uB, "health", "fact", "Allergic to shellfish", "test"); err != nil {
		t.Fatal(err)
	}

	// Alice's query must find her own row (stemmed match on "allergic").
	hits, err := db.SearchMemory(ctx, uA, "am I allergic to anything?", "", "", "", 8)
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
	none, err := db.SearchMemory(ctx, uA, "allergic", "work", "", "", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("topic filter failed: expected 0, got %d", len(none))
	}
}
