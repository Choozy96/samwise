package store

import (
	"context"
	"testing"
	"time"
)

// TestUsageReport seeds runs for two users/models and checks the grouping math,
// the time filter, and the one-row total.
func TestUsageReport(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	u1, _ := db.CreateUser(ctx, "alice", "h", true)
	u2, _ := db.CreateUser(ctx, "bob", "h", false)

	add := func(uid int64, model string, in, out int64, cost float64, started string) {
		if _, err := db.Exec(`INSERT INTO runs (user_id, runtime, model, status, input_tokens, output_tokens, cost_usd, started_at)
			VALUES (?, 'claude-headless', ?, 'success', ?, ?, ?, ?)`, uid, model, in, out, cost, started); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	f := "2006-01-02 15:04:05"
	recent := now.Add(-1 * time.Hour).Format(f)
	old := now.Add(-48 * time.Hour).Format(f)
	add(u1, "claude-fable-5", 100, 10, 0.5, recent)
	add(u1, "claude-opus-4-8", 200, 20, 1.0, recent)
	add(u2, "claude-fable-5", 400, 40, 2.0, recent)
	add(u2, "claude-fable-5", 800, 80, 4.0, old) // outside the 24h window

	from := now.Add(-24 * time.Hour).Format(f)

	// By user: two rows; bob (higher cost) first; old run excluded.
	rows, err := db.UsageReport(ctx, from, "", "user")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Username != "bob" || rows[0].CostUSD != 2.0 || rows[1].InputTokens != 300 {
		t.Fatalf("by user unexpected: %+v", rows)
	}

	// By model: fable aggregates across users.
	rows, err = db.UsageReport(ctx, from, "", "model")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Model != "claude-fable-5" || rows[0].InputTokens != 500 {
		t.Fatalf("by model unexpected: %+v", rows)
	}

	// Total: one row with everything in-window.
	rows, err = db.UsageReport(ctx, from, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Runs != 3 || rows[0].CostUSD != 3.5 {
		t.Fatalf("total unexpected: %+v", rows)
	}

	// Unknown grouping errors.
	if _, err := db.UsageReport(ctx, from, "", "nope"); err == nil {
		t.Error("unknown group_by should error")
	}
}
