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
	NextLocal    string
	Summary      string
	DeliverySel  string // form select value: "" | "web" | "tg"
	DeliveryChat string // chat id, when DeliverySel == "tg"
}

// chatHint is one Telegram chat the user is paired to, offered as a datalist
// suggestion under the delivery chat-id field.
type chatHint struct {
	ChatID string
	Label  string
}

// chatHints lists the Telegram chats a user may deliver to — their own DM and
// any group the bot is paired to — as id + human label. These are the only
// targets resolveTGChat will accept, so the field doubles as the allow-list.
func (s *Server) chatHints(r *http.Request, userID int64) []chatHint {
	ids, _ := s.db.ListIdentitiesByUser(r.Context(), userID, "telegram")
	var out []chatHint
	for _, id := range ids {
		if id.ChatID == "" {
			continue
		}
		label := "DM"
		if strings.HasPrefix(id.ChatID, "-") {
			label = "group"
		}
		out = append(out, chatHint{ChatID: id.ChatID, Label: label})
	}
	return out
}

// resolveTGChat turns a user-entered Telegram chat/user id into a stored
// "tg:<botID>:<chatID>" target, resolving which bot owns that chat from the
// user's identities. It accepts ONLY a chat the user is actually paired to, so
// nobody can steer a job at a chat (or another user's DM) they don't belong to.
func (s *Server) resolveTGChat(r *http.Request, userID int64, chatID string) (string, bool) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return "", false
	}
	ids, _ := s.db.ListIdentitiesByUser(r.Context(), userID, "telegram")
	for _, id := range ids {
		if id.ChatID == chatID {
			return "tg:" + strconv.FormatInt(id.BotID, 10) + ":" + chatID, true
		}
	}
	return "", false
}

// parseDeliveryForm reads the delivery controls from a job form and returns the
// stored delivery target ("", "web", or "tg:<bot>:<chat>"). A non-empty error
// string means the submitted Telegram chat id wasn't valid and the form should
// be redisplayed.
func (s *Server) parseDeliveryForm(r *http.Request, userID int64) (string, string) {
	switch strings.TrimSpace(r.FormValue("delivery")) {
	case "web":
		return "web", ""
	case "tg":
		chat := strings.TrimSpace(r.FormValue("delivery_chat"))
		if chat == "" {
			return "", "Enter a Telegram chat or user id, or pick another destination."
		}
		if v, ok := s.resolveTGChat(r, userID, chat); ok {
			return v, ""
		}
		return "", "That isn't a Telegram chat you're paired to. The bot must be paired to it first (DM it, or add it to the group)."
	default:
		return "", "" // default channel
	}
}

// splitDelivery breaks a stored delivery value back into the form's select value
// ("", "web", "tg") and the chat id, for redisplaying an existing job.
func splitDelivery(stored string) (sel, chat string) {
	switch {
	case stored == "web":
		return "web", ""
	case strings.HasPrefix(stored, "tg:"):
		if _, c, ok := parseStoredTG(stored); ok {
			return "tg", c
		}
		return "tg", ""
	default:
		return "", ""
	}
}

// parseStoredTG splits "tg:<botID>:<chatID>" into its parts.
func parseStoredTG(s string) (bot, chat string, ok bool) {
	parts := strings.SplitN(s, ":", 3)
	if len(parts) != 3 || parts[0] != "tg" {
		return "", "", false
	}
	return parts[1], parts[2], true
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
		v.DeliverySel, v.DeliveryChat = splitDelivery(payloadField(j, "delivery"))
		if t, perr := time.Parse(time.RFC3339, j.NextFireUTC); perr == nil {
			v.NextLocal = t.In(loc).Format("Mon 2006-01-02 15:04 MST")
		}
		views = append(views, v)
	}
	data := pageData{
		"Title": "Cron jobs", "Jobs": views, "TZ": settings.Timezone,
		"ChatHints": s.chatHints(r, u.ID),
	}
	switch r.URL.Query().Get("msg") {
	case "created":
		data["Flash"], data["FlashKind"] = "Job created.", "ok"
	case "updated":
		data["Flash"], data["FlashKind"] = "Job updated.", "ok"
	case "badspec":
		data["Flash"], data["FlashKind"] = "Could not schedule that — check the schedule format.", "error"
	case "baddelivery":
		data["Flash"], data["FlashKind"] = "That isn't a Telegram chat you're paired to — pair the bot to it first.", "error"
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
		p := map[string]string{"prompt": body}
		d, derr := s.parseDeliveryForm(r, u.ID)
		if derr != "" {
			http.Redirect(w, r, "/jobs?msg=baddelivery", http.StatusSeeOther)
			return
		}
		if d != "" {
			p["delivery"] = d
		}
		payload, _ = json.Marshal(p)
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
		d, derr := s.parseDeliveryForm(r, u.ID)
		if derr != "" {
			http.Redirect(w, r, "/jobs?msg=baddelivery", http.StatusSeeOther)
			return
		}
		if d != "" {
			p["delivery"] = d
		} else {
			delete(p, "delivery")
		}
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

// handleJobEnable toggles a job's enabled flag straight from the list (a
// standalone checkbox), so pausing/resuming doesn't require opening the editor.
// The submitted "enabled" is the target state. Re-enabling recomputes the next
// fire time so a job paused past its slot doesn't immediately back-fire.
func (s *Server) handleJobEnable(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	job, err := s.db.GetJob(r.Context(), u.ID, id)
	if err != nil {
		http.Redirect(w, r, "/jobs", http.StatusSeeOther)
		return
	}
	enable := r.FormValue("enabled") == "1"
	if enable {
		settings, _ := s.db.GetSettings(r.Context(), u.ID)
		loc := schedule.LocationFor(job.TZMode, job.TZRef, settings.Timezone)
		if spec, perr := schedule.Parse(job.ScheduleSpec); perr == nil {
			if next, ok := schedule.NextFireUTC(spec, loc, time.Now()); ok {
				job.NextFireUTC = next.UTC().Format(time.RFC3339)
			}
		}
	}
	job.Enabled = enable
	if err := s.db.UpdateJob(r.Context(), *job); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/jobs", http.StatusSeeOther)
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

// payloadField pulls a single string field out of a job's JSON payload.
func payloadField(j store.Job, key string) string {
	var p map[string]string
	if err := json.Unmarshal([]byte(j.Payload), &p); err != nil {
		return ""
	}
	return p[key]
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
