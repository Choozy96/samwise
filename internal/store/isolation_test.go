package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestCrossUserIsolation is the core multi-user security check: one user can
// never read or delete another user's memory, jobs, agents, or secrets through
// the data layer.
func TestCrossUserIsolation(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	alice, _ := db.CreateUser(ctx, "alice", "h", false)
	bob, _ := db.CreateUser(ctx, "bob", "h", false)

	// ── memory ───────────────────────────────────────────────────────────────
	db.SaveSemantic(ctx, alice, 0, "work", "fact", "alice uses rust", "assistant")
	bobMem, _ := db.SaveSemantic(ctx, bob, 0, "work", "fact", "bob uses golang widgets", "assistant")

	// Alice's search never returns Bob's content.
	hits, _ := db.SearchMemory(ctx, alice, AllAgents, "golang widgets", "", "", "", 10)
	for _, h := range hits {
		if strings.Contains(h.Content, "bob") {
			t.Errorf("alice saw bob's memory: %q", h.Content)
		}
	}
	// Alice cannot delete Bob's memory; Bob can.
	if ok, _ := db.ForgetSemantic(ctx, alice, AllAgents, bobMem); ok {
		t.Error("alice deleted bob's memory")
	}
	if ok, _ := db.ForgetSemantic(ctx, bob, AllAgents, bobMem); !ok {
		t.Error("bob could not delete his own memory")
	}

	// ── jobs ─────────────────────────────────────────────────────────────────
	bobJob, _ := db.CreateJob(ctx, Job{UserID: bob, Name: "j", Type: "agent_run",
		ScheduleSpec: "daily@08:00", TZMode: "user_local", Payload: "{}", Enabled: true})
	if _, err := db.GetJob(ctx, alice, bobJob); !errors.Is(err, ErrNotFound) {
		t.Errorf("alice fetched bob's job: %v", err)
	}
	_ = db.DeleteJob(ctx, alice, bobJob) // must be a no-op
	if _, err := db.GetJob(ctx, bob, bobJob); err != nil {
		t.Errorf("bob's job was deleted by alice: %v", err)
	}

	// ── agents ───────────────────────────────────────────────────────────────
	bobAgent, _ := db.CreateAgent(ctx, Agent{UserID: bob, Name: "bobagent", Enabled: true})
	if _, err := db.GetAgent(ctx, alice, bobAgent); !errors.Is(err, ErrNotFound) {
		t.Errorf("alice fetched bob's agent: %v", err)
	}

	// ── secrets ──────────────────────────────────────────────────────────────
	_ = db.SetSecret(ctx, bob, "BOB_TOKEN", "ciphertext", "value")
	for _, s := range mustList(t, db, alice) {
		if s.Name == "BOB_TOKEN" {
			t.Error("alice saw bob's secret")
		}
	}
}

func mustList(t *testing.T, db *DB, userID int64) []Secret {
	t.Helper()
	s, err := db.ListSecrets(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestSearchMemoryNoInjection: a malicious search query can't break out of the
// FTS layer to error, inject SQL, or read another user's rows. buildMatch reduces
// any input to quoted alphanumeric tokens, and the match is a bound parameter.
func TestSearchMemoryNoInjection(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	alice, _ := db.CreateUser(ctx, "alice", "h", false)
	bob, _ := db.CreateUser(ctx, "bob", "h", false)
	db.SaveSemantic(ctx, alice, 0, "t", "fact", "alice apple", "assistant")
	db.SaveSemantic(ctx, bob, 0, "t", "fact", "bob banana", "assistant")

	for _, q := range []string{
		`apple" OR "1"="1`,
		`apple'; SELECT * FROM users; --`,
		`apple OR user_id = 2`,
		`banana`, // alice searching for bob's word must still see nothing of bob's
	} {
		hits, err := db.SearchMemory(ctx, alice, AllAgents, q, "", "", "", 10)
		if err != nil {
			t.Errorf("query %q errored (should be sanitized, not fail): %v", q, err)
		}
		for _, h := range hits {
			if strings.Contains(h.Content, "bob") || strings.Contains(h.Content, "banana") {
				t.Errorf("query %q leaked bob's data: %q", q, h.Content)
			}
		}
	}

	// buildMatch emits only quoted tokens joined by OR — no SQL/FTS metacharacters.
	m := buildMatch(`apple"; DROP TABLE users; -- (x)`)
	for _, bad := range []string{";", "'", "--", "(", ")", "="} {
		if strings.Contains(m, bad) {
			t.Errorf("buildMatch leaked metacharacter %q in %q", bad, m)
		}
	}
	if !strings.Contains(m, `"apple"`) {
		t.Errorf("buildMatch dropped the real token: %q", m)
	}
}
