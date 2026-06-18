package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"samwise/internal/schedule"
	"samwise/internal/store"
)

// jobView is a job decorated for display (next fire in local time, parsed payload).
type jobView struct {
	store.Job
	NextLocal string
	Summary   string
}

// handleJobs lists the user's scheduled jobs and a creation form.
func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	jobs, err := s.db.ListJobs(r.Context(), u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	settings, _ := s.db.GetSettings(r.Context(), u.ID)
	loc := schedule.LocationFor("user_local", "", settings.Timezone)

	views := make([]jobView, 0, len(jobs))
	for _, j := range jobs {
		v := jobView{Job: j, Summary: payloadSummary(j)}
		if t, perr := time.Parse(time.RFC3339, j.NextFireUTC); perr == nil {
			v.NextLocal = t.In(loc).Format("Mon 2006-01-02 15:04 MST")
		}
		views = append(views, v)
	}
	data := pageData{"Title": "Cron jobs", "Jobs": views, "TZ": settings.Timezone}
	switch r.URL.Query().Get("msg") {
	case "created":
		data["Flash"], data["FlashKind"] = "Job created.", "ok"
	case "updated":
		data["Flash"], data["FlashKind"] = "Job updated.", "ok"
	case "badspec":
		data["Flash"], data["FlashKind"] = "Could not schedule that — check the schedule format.", "error"
	}
	s.render(w, r, "jobs", data)
}

// handleJobCreate creates an agent_run or direct_message job from the portal.
func (s *Server) handleJobCreate(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	settings, err := s.db.GetSettings(r.Context(), u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	jobType := pick(r.FormValue("type"), []string{"agent_run", "direct_message"}, "direct_message")
	specStr := strings.TrimSpace(r.FormValue("schedule_spec"))
	body := strings.TrimSpace(r.FormValue("body"))

	spec, perr := schedule.Parse(specStr)
	if perr != nil || body == "" {
		http.Redirect(w, r, "/jobs?msg=badspec", http.StatusSeeOther)
		return
	}
	loc := schedule.LocationFor("user_local", "", settings.Timezone)
	next, ok := schedule.NextFireUTC(spec, loc, time.Now())
	if !ok {
		http.Redirect(w, r, "/jobs?msg=badspec", http.StatusSeeOther)
		return
	}

	var payload []byte
	if jobType == "agent_run" {
		payload, _ = json.Marshal(map[string]string{"prompt": body})
	} else {
		payload, _ = json.Marshal(map[string]string{"message": body})
	}

	_, err = s.db.CreateJob(r.Context(), store.Job{
		UserID:       u.ID,
		Name:         name,
		Type:         jobType,
		ScheduleSpec: specStr,
		TZMode:       "user_local",
		Payload:      string(payload),
		Enabled:      true,
		CatchUp:      true,
		NextFireUTC:  next.UTC().Format(time.RFC3339),
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/jobs?msg=created", http.StatusSeeOther)
}

// handleJobUpdate edits a job's name, schedule, body, and enabled flag, preserving
// other payload fields (e.g. an agent_run's skill/agent). The schedule change
// recomputes the next fire time.
func (s *Server) handleJobUpdate(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	job, err := s.db.GetJob(r.Context(), u.ID, id)
	if err != nil {
		http.Redirect(w, r, "/jobs", http.StatusSeeOther)
		return
	}
	settings, _ := s.db.GetSettings(r.Context(), u.ID)

	specStr := strings.TrimSpace(r.FormValue("schedule_spec"))
	body := strings.TrimSpace(r.FormValue("body"))
	spec, perr := schedule.Parse(specStr)
	if perr != nil || body == "" {
		http.Redirect(w, r, "/jobs?msg=badspec", http.StatusSeeOther)
		return
	}
	loc := schedule.LocationFor(job.TZMode, job.TZRef, settings.Timezone)
	next, ok := schedule.NextFireUTC(spec, loc, time.Now())
	if !ok {
		http.Redirect(w, r, "/jobs?msg=badspec", http.StatusSeeOther)
		return
	}

	// Merge the edited body into the existing payload so skill/agent survive.
	p := map[string]string{}
	_ = json.Unmarshal([]byte(job.Payload), &p)
	if job.Type == "agent_run" {
		p["prompt"] = body
	} else {
		p["message"] = body
	}
	payload, _ := json.Marshal(p)

	job.Name = strings.TrimSpace(r.FormValue("name"))
	job.ScheduleSpec = specStr
	job.Payload = string(payload)
	job.Enabled = r.FormValue("enabled") == "1"
	job.NextFireUTC = next.UTC().Format(time.RFC3339)
	if err := s.db.UpdateJob(r.Context(), *job); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/jobs?msg=updated", http.StatusSeeOther)
}

// handleJobDelete removes a job the user owns.
func (s *Server) handleJobDelete(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	if id, err := strconv.ParseInt(r.FormValue("id"), 10, 64); err == nil {
		if err := s.db.DeleteJob(r.Context(), u.ID, id); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	http.Redirect(w, r, "/jobs", http.StatusSeeOther)
}

func payloadSummary(j store.Job) string {
	var p map[string]string
	if err := json.Unmarshal([]byte(j.Payload), &p); err != nil {
		return ""
	}
	if v := p["message"]; v != "" {
		return v
	}
	if v := p["prompt"]; v != "" {
		return v
	}
	if v := p["kind"]; v != "" {
		return "maintenance: " + v
	}
	return ""
}
