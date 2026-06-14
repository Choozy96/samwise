package mcpserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"samwise/internal/store"
)

// resultText pulls the text out of a tool result for assertions.
func resultText(r *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func newJobHandlers(t *testing.T) (*handlers, context.Context) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	uid, err := db.CreateUser(ctx, "alice", "h", true)
	if err != nil {
		t.Fatal(err)
	}
	return &handlers{db: db, userID: uid}, ctx
}

// TestJobToolsLifecycle exercises create -> list -> update -> delete and checks
// the persisted job reflects each change.
func TestJobToolsLifecycle(t *testing.T) {
	h, ctx := newJobHandlers(t)

	// Create.
	if _, _, err := h.jobCreate(ctx, nil, jobCreateIn{
		Name: "Morning briefing", Schedule: "daily@08:00",
		Prompt: "Give me my briefing", Skill: "daily-briefing",
	}); err != nil {
		t.Fatal(err)
	}
	jobs, err := h.db.ListJobs(ctx, h.userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Type != "agent_run" || jobs[0].ScheduleSpec != "daily@08:00" {
		t.Fatalf("create wrong: %+v", jobs)
	}
	id := jobs[0].ID
	if !strings.Contains(jobs[0].Payload, "daily-briefing") {
		t.Errorf("skill not stored in payload: %s", jobs[0].Payload)
	}

	// List shows it.
	res, _, _ := h.jobList(ctx, nil, emptyIn{})
	if txt := resultText(res); !strings.Contains(txt, "Morning briefing") || !strings.Contains(txt, "daily-briefing") {
		t.Errorf("list missing job: %q", txt)
	}

	// Update schedule + pause + clear skill.
	paused := false
	if _, _, err := h.jobUpdate(ctx, nil, jobUpdateIn{
		ID: id, Schedule: "daily@09:30", Skill: "-", Enabled: &paused,
	}); err != nil {
		t.Fatal(err)
	}
	j, _ := h.db.GetJob(ctx, h.userID, id)
	if j.ScheduleSpec != "daily@09:30" {
		t.Errorf("schedule not updated: %s", j.ScheduleSpec)
	}
	if j.Enabled {
		t.Error("job should be paused")
	}
	if strings.Contains(j.Payload, "daily-briefing") {
		t.Errorf("skill should be cleared: %s", j.Payload)
	}

	// Reminders (direct_message) must be invisible to job tools.
	if _, _, err := h.reminderSet(ctx, nil, reminderSetIn{Message: "ping", Daily: "10:00"}); err != nil {
		t.Fatal(err)
	}
	res, _, _ = h.jobList(ctx, nil, emptyIn{})
	if strings.Contains(resultText(res), "ping") {
		t.Error("job_list leaked a reminder")
	}

	// Delete.
	if _, _, err := h.jobDelete(ctx, nil, jobDeleteIn{ID: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.GetJob(ctx, h.userID, id); err == nil {
		t.Error("job not deleted")
	}
}
