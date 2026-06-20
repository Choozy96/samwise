package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"samwise/internal/schedule"
	"samwise/internal/store"
)

// Reminder tools create/list/cancel direct_message jobs: a reminder
// is a "ping me" with no agent and no tokens, distinct from a task in the user's
// task system.

type reminderSetIn struct {
	Message string `json:"message" jsonschema:"the reminder text to send the user"`
	At      string `json:"at,omitempty" jsonschema:"one-shot local time, format 'YYYY-MM-DD HH:MM'"`
	Daily   string `json:"daily,omitempty" jsonschema:"for a daily repeating reminder, the local time 'HH:MM'"`
}

type reminderCancelIn struct {
	ID int64 `json:"id" jsonschema:"the reminder id to cancel (from reminder_list)"`
}

func (h *handlers) registerReminders(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "reminder_set",
		Description: "Set a reminder that pings the user at a time. Provide 'at' for a one-shot (YYYY-MM-DD HH:MM in the user's local time) OR 'daily' (HH:MM) for a repeating daily reminder. Compute the absolute time from the user's local time given in your context.",
	}, h.reminderSet)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "reminder_list",
		Description: "List the user's active reminders with their ids and next fire times.",
	}, h.reminderList)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "reminder_cancel",
		Description: "Cancel a reminder by id.",
	}, h.reminderCancel)
}

func (h *handlers) reminderSet(ctx context.Context, _ *mcp.CallToolRequest, in reminderSetIn) (*mcp.CallToolResult, any, error) {
	if h.readOnly {
		return h.denyWrite("reminder_set"), nil, nil
	}
	msg := strings.TrimSpace(in.Message)
	if msg == "" {
		return h.fail("reminder_set", "", "message is required"), nil, nil
	}
	settings, err := h.db.GetSettings(ctx, h.userID)
	if err != nil {
		return h.fail("reminder_set", "", err.Error()), nil, nil
	}
	loc := schedule.LocationFor("user_local", "", settings.Timezone)

	var specStr string
	switch {
	case strings.TrimSpace(in.Daily) != "":
		specStr = "daily@" + strings.TrimSpace(in.Daily)
	case strings.TrimSpace(in.At) != "":
		t, perr := time.ParseInLocation("2006-01-02 15:04", strings.TrimSpace(in.At), loc)
		if perr != nil {
			return h.fail("reminder_set", "", "bad 'at' time; use YYYY-MM-DD HH:MM"), nil, nil
		}
		specStr = "once@" + t.Format("2006-01-02T15:04")
	default:
		return h.fail("reminder_set", "", "provide 'at' or 'daily'"), nil, nil
	}

	spec, perr := schedule.Parse(specStr)
	if perr != nil {
		return h.fail("reminder_set", "", perr.Error()), nil, nil
	}
	next, ok := schedule.NextFireUTC(spec, loc, time.Now())
	if !ok {
		return h.fail("reminder_set", "", "that time is in the past"), nil, nil
	}

	payload, _ := json.Marshal(map[string]string{"message": msg})
	id, err := h.db.CreateJob(ctx, store.Job{
		UserID:       h.userID,
		Name:         "reminder",
		Type:         "direct_message",
		ScheduleSpec: specStr,
		TZMode:       "user_local",
		Payload:      string(payload),
		Enabled:      true,
		CatchUp:      true, // a dropped reminder is the worst failure
		NextFireUTC:  next.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return h.fail("reminder_set", "", err.Error()), nil, nil
	}
	h.audit("reminder_set", "spec="+specStr, "ok")
	return textResult(fmt.Sprintf("Reminder #%d set for %s.", id, next.In(loc).Format("Mon 2006-01-02 15:04 MST"))), nil, nil
}

func (h *handlers) reminderList(ctx context.Context, _ *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, any, error) {
	jobs, err := h.db.ListJobs(ctx, h.userID)
	if err != nil {
		return h.fail("reminder_list", "", err.Error()), nil, nil
	}
	settings, _ := h.db.GetSettings(ctx, h.userID)
	loc := schedule.LocationFor("user_local", "", settings.Timezone)

	var b strings.Builder
	n := 0
	for _, j := range jobs {
		if j.Type != "direct_message" || !j.Enabled {
			continue
		}
		n++
		var p struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal([]byte(j.Payload), &p)
		when := j.NextFireUTC
		if t, perr := time.Parse(time.RFC3339, j.NextFireUTC); perr == nil {
			when = t.In(loc).Format("Mon 2006-01-02 15:04 MST")
		}
		fmt.Fprintf(&b, "- #%d at %s: %s\n", j.ID, when, p.Message)
	}
	h.audit("reminder_list", fmt.Sprintf("n=%d", n), "ok")
	if n == 0 {
		return textResult("No active reminders."), nil, nil
	}
	return textResult(strings.TrimRight(b.String(), "\n")), nil, nil
}

func (h *handlers) reminderCancel(ctx context.Context, _ *mcp.CallToolRequest, in reminderCancelIn) (*mcp.CallToolResult, any, error) {
	if h.readOnly {
		return h.denyWrite("reminder_cancel"), nil, nil
	}
	j, err := h.db.GetJob(ctx, h.userID, in.ID)
	if err != nil {
		return h.fail("reminder_cancel", fmt.Sprintf("id=%d", in.ID), "no such reminder"), nil, nil
	}
	if j.Type != "direct_message" {
		return h.fail("reminder_cancel", fmt.Sprintf("id=%d", in.ID), "not a reminder"), nil, nil
	}
	if err := h.db.DeleteJob(ctx, h.userID, in.ID); err != nil {
		return h.fail("reminder_cancel", fmt.Sprintf("id=%d", in.ID), err.Error()), nil, nil
	}
	h.audit("reminder_cancel", fmt.Sprintf("id=%d", in.ID), "ok")
	return textResult(fmt.Sprintf("Cancelled reminder #%d.", in.ID)), nil, nil
}
