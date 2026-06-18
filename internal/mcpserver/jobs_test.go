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

	// Delete (happy path): result is success and the row is gone.
	res, _, err = h.jobDelete(ctx, nil, jobDeleteIn{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Errorf("delete should succeed, got error result: %q", resultText(res))
	}
	if _, err := h.db.GetJob(ctx, h.userID, id); err == nil {
		t.Error("job not deleted")
	}
}

// TestJobDeleteGuards covers the delete error paths the lifecycle test skips:
// deleting a non-existent id, deleting a reminder via job_delete (must refuse and
// leave it), and cancelling a job via reminder_cancel (must refuse and leave it).
func TestJobDeleteGuards(t *testing.T) {
	h, ctx := newJobHandlers(t)

	// Non-existent id → error result, not a crash.
	res, _, err := h.jobDelete(ctx, nil, jobDeleteIn{ID: 9999})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Errorf("delete of missing id should be an error result, got: %q", resultText(res))
	}

	// One agent_run job and one reminder.
	if _, _, err := h.jobCreate(ctx, nil, jobCreateIn{Name: "J", Schedule: "daily@08:00", Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.reminderSet(ctx, nil, reminderSetIn{Message: "ping", Daily: "10:00"}); err != nil {
		t.Fatal(err)
	}
	jobs, _ := h.db.ListJobs(ctx, h.userID)
	var jobID, remID int64
	for _, j := range jobs {
		switch j.Type {
		case "agent_run":
			jobID = j.ID
		case "direct_message":
			remID = j.ID
		}
	}
	if jobID == 0 || remID == 0 {
		t.Fatalf("setup: jobID=%d remID=%d jobs=%+v", jobID, remID, jobs)
	}

	// job_delete must refuse a reminder and leave it intact.
	res, _, _ = h.jobDelete(ctx, nil, jobDeleteIn{ID: remID})
	if !res.IsError {
		t.Errorf("job_delete on a reminder should be refused, got: %q", resultText(res))
	}
	if _, err := h.db.GetJob(ctx, h.userID, remID); err != nil {
		t.Errorf("reminder should survive a refused job_delete: %v", err)
	}

	// reminder_cancel must refuse an agent_run job and leave it intact.
	res, _, _ = h.reminderCancel(ctx, nil, reminderCancelIn{ID: jobID})
	if !res.IsError {
		t.Errorf("reminder_cancel on a job should be refused, got: %q", resultText(res))
	}
	if _, err := h.db.GetJob(ctx, h.userID, jobID); err != nil {
		t.Errorf("job should survive a refused reminder_cancel: %v", err)
	}

	// Each correct tool deletes its own kind.
	if res, _, _ = h.reminderCancel(ctx, nil, reminderCancelIn{ID: remID}); res.IsError {
		t.Errorf("reminder_cancel on a reminder failed: %q", resultText(res))
	}
	if res, _, _ = h.jobDelete(ctx, nil, jobDeleteIn{ID: jobID}); res.IsError {
		t.Errorf("job_delete on a job failed: %q", resultText(res))
	}
	if jobs, _ := h.db.ListJobs(ctx, h.userID); len(jobs) != 0 {
		t.Errorf("all jobs should be gone, have: %+v", jobs)
	}
}
