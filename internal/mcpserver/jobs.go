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

// Job tools let the agent create and manage its own recurring scheduled jobs
// (type=agent_run): a prompt — optionally backed by a skill — that runs the agent
// on a schedule and delivers the result. Distinct from reminder_* (a plain ping
// with no agent work) and scoped so these tools never touch reminders.

type jobCreateIn struct {
	Name     string `json:"name" jsonschema:"short label for the job, e.g. 'Morning briefing'"`
	Schedule string `json:"schedule" jsonschema:"when to run, in the user's local time: 'daily@HH:MM' or 'weekly@<Day> HH:MM' (24h), e.g. 'daily@08:00' or 'weekly@Sun 09:00'"`
	Prompt   string `json:"prompt" jsonschema:"the instruction to run at that time; its final output is delivered to the user"`
	Skill    string `json:"skill,omitempty" jsonschema:"optional: name of an installed skill to load into the run"`
	Agent    string `json:"agent,omitempty" jsonschema:"optional: name of the agent persona to run as (defaults to the active agent)"`
	Delivery string `json:"delivery,omitempty" jsonschema:"where to deliver the result: 'here' (this chat) or 'web' (the web portal). Omit to use the user's default delivery channel."`
}

type jobUpdateIn struct {
	ID       int64  `json:"id" jsonschema:"the job id to change (from job_list)"`
	Name     string `json:"name,omitempty" jsonschema:"new name"`
	Schedule string `json:"schedule,omitempty" jsonschema:"new schedule, e.g. 'daily@09:00' or 'weekly@Mon 07:30'"`
	Prompt   string `json:"prompt,omitempty" jsonschema:"new prompt"`
	Skill    string `json:"skill,omitempty" jsonschema:"new skill name; pass '-' to clear it"`
	Agent    string `json:"agent,omitempty" jsonschema:"new agent name; pass '-' to clear it"`
	Delivery string `json:"delivery,omitempty" jsonschema:"new delivery destination: 'here' (this chat), 'web', or '-' to reset to the user's default"`
	Enabled  *bool  `json:"enabled,omitempty" jsonschema:"set false to pause the job, true to resume it"`
}

type jobDeleteIn struct {
	ID int64 `json:"id" jsonschema:"the job id to delete (from job_list)"`
}

// agentRunPayload is the JSON stored in jobs.payload for type=agent_run. Delivery
// is "" (the user's default channel), "web", or "tg:<botID>:<chatID>".
type agentRunPayload struct {
	Prompt   string `json:"prompt"`
	Skill    string `json:"skill,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Delivery string `json:"delivery,omitempty"`
}

// resolveDelivery turns a "here"/"web"/"" tool argument into a stored delivery
// target. "here" becomes this run's Telegram chat (or web when there is none).
func (h *handlers) resolveDelivery(arg string) string {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "web":
		return "web"
	case "here":
		if h.originChatID != 0 {
			return fmt.Sprintf("tg:%d:%d", h.originBotID, h.originChatID)
		}
		return "web" // created from the web portal → "here" means web
	default:
		return "" // user's default delivery channel
	}
}

func (h *handlers) registerJobs(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "job_create",
		Description: "Create a recurring scheduled job that runs you (the agent) on a schedule and delivers the result to the user — e.g. a daily briefing or an end-of-day report. Optionally load a skill. For a simple timed ping with no agent work, use reminder_set instead.",
	}, h.jobCreate)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "job_list",
		Description: "List the user's recurring scheduled jobs (agent runs) with their ids, schedules, next fire times, and skills.",
	}, h.jobList)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "job_update",
		Description: "Change a scheduled job by id: its name, schedule, prompt, skill, agent, or enabled state. Only the fields you provide change.",
	}, h.jobUpdate)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "job_delete",
		Description: "Delete a scheduled job by id.",
	}, h.jobDelete)
}

func (h *handlers) jobCreate(ctx context.Context, _ *mcp.CallToolRequest, in jobCreateIn) (*mcp.CallToolResult, any, error) {
	if h.readOnly {
		return h.denyWrite("job_create"), nil, nil
	}
	name := strings.TrimSpace(in.Name)
	prompt := strings.TrimSpace(in.Prompt)
	specStr := strings.TrimSpace(in.Schedule)
	if name == "" || prompt == "" || specStr == "" {
		return h.fail("job_create", name, "name, schedule, and prompt are all required"), nil, nil
	}

	loc, next, err := h.resolveSchedule(ctx, specStr)
	if err != nil {
		return h.fail("job_create", specStr, err.Error()), nil, nil
	}

	payload, _ := json.Marshal(agentRunPayload{
		Prompt: prompt, Skill: strings.TrimSpace(in.Skill), Agent: strings.TrimSpace(in.Agent),
		Delivery: h.resolveDelivery(in.Delivery),
	})
	id, err := h.db.CreateJob(ctx, store.Job{
		UserID:       h.userID,
		Name:         name,
		Type:         "agent_run",
		ScheduleSpec: specStr,
		TZMode:       "user_local",
		Payload:      string(payload),
		Enabled:      true,
		CatchUp:      true,
		NextFireUTC:  next.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return h.fail("job_create", name, err.Error()), nil, nil
	}
	h.audit("job_create", "spec="+specStr, "ok")
	return textResult(fmt.Sprintf("Created job #%d %q — next run %s.",
		id, name, next.In(loc).Format("Mon 2006-01-02 15:04 MST"))), nil, nil
}

func (h *handlers) jobList(ctx context.Context, _ *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, any, error) {
	jobs, err := h.db.ListJobs(ctx, h.userID)
	if err != nil {
		return h.fail("job_list", "", err.Error()), nil, nil
	}
	settings, _ := h.db.GetSettings(ctx, h.userID)
	loc := schedule.LocationFor("user_local", "", settings.Timezone)

	var b strings.Builder
	n := 0
	for _, j := range jobs {
		if j.Type != "agent_run" {
			continue
		}
		n++
		var p agentRunPayload
		_ = json.Unmarshal([]byte(j.Payload), &p)
		when := j.NextFireUTC
		if t, perr := time.Parse(time.RFC3339, j.NextFireUTC); perr == nil {
			when = t.In(loc).Format("Mon 2006-01-02 15:04 MST")
		}
		status := ""
		if !j.Enabled {
			status = " [paused]"
		}
		skill := ""
		if p.Skill != "" {
			skill = " · skill=" + p.Skill
		}
		fmt.Fprintf(&b, "- #%d %q [%s] next %s%s%s\n", j.ID, j.Name, j.ScheduleSpec, when, skill, status)
	}
	h.audit("job_list", fmt.Sprintf("n=%d", n), "ok")
	if n == 0 {
		return textResult("No scheduled jobs."), nil, nil
	}
	return textResult(strings.TrimRight(b.String(), "\n")), nil, nil
}

func (h *handlers) jobUpdate(ctx context.Context, _ *mcp.CallToolRequest, in jobUpdateIn) (*mcp.CallToolResult, any, error) {
	if h.readOnly {
		return h.denyWrite("job_update"), nil, nil
	}
	j, err := h.db.GetJob(ctx, h.userID, in.ID)
	if err != nil {
		return h.fail("job_update", fmt.Sprintf("id=%d", in.ID), "no such job"), nil, nil
	}
	if j.Type != "agent_run" {
		return h.fail("job_update", fmt.Sprintf("id=%d", in.ID), "not a scheduled job (use reminder tools for reminders)"), nil, nil
	}

	var p agentRunPayload
	_ = json.Unmarshal([]byte(j.Payload), &p)

	if s := strings.TrimSpace(in.Name); s != "" {
		j.Name = s
	}
	if s := strings.TrimSpace(in.Prompt); s != "" {
		p.Prompt = s
	}
	if s := strings.TrimSpace(in.Skill); s != "" {
		p.Skill = clearable(s)
	}
	if s := strings.TrimSpace(in.Agent); s != "" {
		p.Agent = clearable(s)
	}
	if s := strings.TrimSpace(in.Delivery); s != "" {
		if s == "-" {
			p.Delivery = "" // reset to the user's default channel
		} else {
			p.Delivery = h.resolveDelivery(s)
		}
	}
	if in.Enabled != nil {
		j.Enabled = *in.Enabled
	}

	loc := schedule.LocationFor("user_local", "", h.userTimezone(ctx))
	if s := strings.TrimSpace(in.Schedule); s != "" {
		l, next, perr := h.resolveSchedule(ctx, s)
		if perr != nil {
			return h.fail("job_update", s, perr.Error()), nil, nil
		}
		loc = l
		j.ScheduleSpec = s
		j.NextFireUTC = next.UTC().Format(time.RFC3339)
	}

	payload, _ := json.Marshal(p)
	j.Payload = string(payload)
	if err := h.db.UpdateJob(ctx, *j); err != nil {
		return h.fail("job_update", fmt.Sprintf("id=%d", in.ID), err.Error()), nil, nil
	}
	h.audit("job_update", fmt.Sprintf("id=%d", in.ID), "ok")

	when := j.NextFireUTC
	if t, perr := time.Parse(time.RFC3339, j.NextFireUTC); perr == nil {
		when = t.In(loc).Format("Mon 2006-01-02 15:04 MST")
	}
	return textResult(fmt.Sprintf("Updated job #%d %q [%s] — next run %s.", j.ID, j.Name, j.ScheduleSpec, when)), nil, nil
}

func (h *handlers) jobDelete(ctx context.Context, _ *mcp.CallToolRequest, in jobDeleteIn) (*mcp.CallToolResult, any, error) {
	if h.readOnly {
		return h.denyWrite("job_delete"), nil, nil
	}
	j, err := h.db.GetJob(ctx, h.userID, in.ID)
	if err != nil {
		return h.fail("job_delete", fmt.Sprintf("id=%d", in.ID), "no such job"), nil, nil
	}
	if j.Type != "agent_run" {
		return h.fail("job_delete", fmt.Sprintf("id=%d", in.ID), "not a scheduled job (use reminder tools for reminders)"), nil, nil
	}
	if err := h.db.DeleteJob(ctx, h.userID, in.ID); err != nil {
		return h.fail("job_delete", fmt.Sprintf("id=%d", in.ID), err.Error()), nil, nil
	}
	h.audit("job_delete", fmt.Sprintf("id=%d", in.ID), "ok")
	return textResult(fmt.Sprintf("Deleted job #%d.", in.ID)), nil, nil
}

// resolveSchedule parses a schedule_spec, computes the next fire time in the
// user's local timezone, and returns the location + that time.
func (h *handlers) resolveSchedule(ctx context.Context, specStr string) (*time.Location, time.Time, error) {
	loc := schedule.LocationFor("user_local", "", h.userTimezone(ctx))
	spec, err := schedule.Parse(specStr)
	if err != nil {
		return loc, time.Time{}, fmt.Errorf("bad schedule %q; use 'daily@HH:MM' or 'weekly@<Day> HH:MM'", specStr)
	}
	next, ok := schedule.NextFireUTC(spec, loc, time.Now())
	if !ok {
		return loc, time.Time{}, fmt.Errorf("that schedule has no upcoming run")
	}
	return loc, next, nil
}

func (h *handlers) userTimezone(ctx context.Context) string {
	s, err := h.db.GetSettings(ctx, h.userID)
	if err != nil {
		return ""
	}
	return s.Timezone
}

// clearable lets the caller wipe an optional field by passing "-".
func clearable(s string) string {
	if s == "-" {
		return ""
	}
	return s
}
