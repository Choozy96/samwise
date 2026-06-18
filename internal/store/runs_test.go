package store

import (
	"context"
	"testing"
)

// TestUsageSinceTokens verifies token usage is persisted per run and summed by
// type across a window (the metric that replaced dollar cost as the headline).
func TestUsageSinceTokens(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	uid, err := db.CreateUser(ctx, "alice", "h", true)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := db.GetActiveAgent(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := db.GetOrCreateConversation(ctx, uid, "web", agent.ID)
	if err != nil {
		t.Fatal(err)
	}

	r1, _ := db.CreateRun(ctx, uid, conv.ID, "claude-headless", "opus")
	if err := db.FinishRun(ctx, r1, "success", "", RunMetrics{
		DurationMS: 100, InputTokens: 1000, OutputTokens: 200,
		CacheCreationTokens: 5000, CacheReadTokens: 12000,
	}); err != nil {
		t.Fatal(err)
	}
	r2, _ := db.CreateRun(ctx, uid, conv.ID, "claude-headless", "opus")
	if err := db.FinishRun(ctx, r2, "error", "boom", RunMetrics{
		InputTokens: 500, OutputTokens: 50, CacheReadTokens: 3000,
	}); err != nil {
		t.Fatal(err)
	}

	u, err := db.UsageSince(ctx, uid, "2000-01-01 00:00:00")
	if err != nil {
		t.Fatal(err)
	}
	if u.Runs != 2 || u.Errors != 1 {
		t.Errorf("runs/errors: got %d/%d want 2/1", u.Runs, u.Errors)
	}
	if u.InputTokens != 1500 || u.OutputTokens != 250 {
		t.Errorf("in/out tokens: got %d/%d want 1500/250", u.InputTokens, u.OutputTokens)
	}
	if u.CacheCreationTokens != 5000 || u.CacheReadTokens != 15000 {
		t.Errorf("cache tokens: got write=%d read=%d want 5000/15000", u.CacheCreationTokens, u.CacheReadTokens)
	}
}
