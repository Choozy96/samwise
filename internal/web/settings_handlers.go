package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"samwise/internal/auth"
	"samwise/internal/orchestrator"
	"samwise/internal/runtime"
	"samwise/internal/schedule"
	"samwise/internal/store"
)

// handleSettings renders the current user's settings.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	st, err := s.db.GetSettings(r.Context(), u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data := s.settingsData(st)
	switch {
	case r.URL.Query().Get("saved") == "1":
		data["Flash"] = "Settings saved."
		data["FlashKind"] = "ok"
	case r.URL.Query().Get("pw") == "1":
		data["Flash"] = "Password changed."
		data["FlashKind"] = "ok"
	}
	s.render(w, r, "settings", data)
}

// handleChangePassword lets a signed-in user change their own password. It
// requires the current password (so a hijacked session can't silently lock the
// owner out) and a confirmed new password of at least 8 characters.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	current := r.FormValue("current_password")
	next := r.FormValue("new_password")
	confirm := r.FormValue("confirm_password")

	fail := func(msg string) {
		st, err := s.db.GetSettings(r.Context(), u.ID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		s.renderSettingsError(w, r, st, msg)
	}

	// Re-read the user to verify the current password against the live hash.
	full, err := s.db.GetUserByID(r.Context(), u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := auth.VerifyPassword(current, full.PasswordHash); err != nil {
		_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "auth", "password_change", "web ("+s.clientIP(r)+")", "denied")
		fail("Current password is incorrect.")
		return
	}
	if len(next) < 8 {
		fail("New password must be at least 8 characters.")
		return
	}
	if next != confirm {
		fail("New password and confirmation do not match.")
		return
	}
	if next == current {
		fail("New password must differ from the current one.")
		return
	}

	hash, err := auth.HashPassword(next)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.db.UpdatePassword(r.Context(), u.ID, hash); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("password changed", "user_id", u.ID, "username", u.Username)
	_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "auth", "password_change", "web ("+s.clientIP(r)+")", "ok")
	http.Redirect(w, r, "/settings?pw=1", http.StatusSeeOther)
}

// settingsData assembles the template data, including the access-method/model
// catalogs and the currently selected model id.
func (s *Server) settingsData(st *store.Settings) pageData {
	enabled := map[string]bool{}
	for _, name := range strings.Split(st.ExtraTools, ",") {
		if name = strings.TrimSpace(name); name != "" {
			enabled[name] = true
		}
	}
	return pageData{
		"Title":         "Settings",
		"S":             st,
		"Runtimes":      s.orch.RuntimeChoices(),
		"ModelOptions":  s.orch.ModelChoices(),
		"CurrentModel":  orchestrator.ModelHintFor(st.ModelHints, "chat"),
		"AgentToolsOn":  s.cfg.AllowAgentTools,
		"OptionalTools": runtime.OptionalTools,
		"EnabledTools":  enabled,
	}
}

// handleSaveSettings validates and persists settings edits. A timezone change
// must recompute the user's user_local scheduled jobs; the scheduler
// hook for that is added in MVP step 5 — here we persist the value.
func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	st, err := s.db.GetSettings(r.Context(), u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	tz := strings.TrimSpace(r.FormValue("timezone"))
	if _, err := time.LoadLocation(tz); err != nil {
		s.renderSettingsError(w, r, st, "Unknown timezone — use an IANA name like Asia/Singapore.")
		return
	}
	n, err := strconv.Atoi(r.FormValue("transcript_window_n"))
	if err != nil || n < 1 || n > 200 {
		s.renderSettingsError(w, r, st, "Transcript window must be between 1 and 200.")
		return
	}
	k, err := strconv.Atoi(r.FormValue("retrieval_k"))
	if err != nil || k < 0 || k > 50 {
		s.renderSettingsError(w, r, st, "Retrieval K must be between 0 and 50.")
		return
	}

	oldTZ := st.Timezone
	st.Timezone = tz
	// Access method: accept a known runtime that is either available now, or the
	// one already selected (so a previously-set value isn't lost).
	if id, ok := orchestrator.ResolveRuntime(r.FormValue("active_runtime")); ok && (s.orch.IsRuntimeAvailable(id) || id == st.ActiveRuntime) {
		st.ActiveRuntime = id
	}
	// Model: resolve an alias from the dropdown, or accept a raw model id.
	if modelID, ok := orchestrator.ResolveModel(r.FormValue("model")); ok {
		st.ModelHints = orchestrator.SetChatModel(st.ModelHints, modelID)
	} else if raw := strings.TrimSpace(r.FormValue("model")); raw != "" {
		st.ModelHints = orchestrator.SetChatModel(st.ModelHints, raw)
	}
	st.DeliveryChannel = pick(r.FormValue("delivery_channel"), []string{"web", "telegram"}, st.DeliveryChannel)
	st.TgFormat = pick(r.FormValue("tg_format"), []string{"markdown", "html", "plain"}, st.TgFormat)
	st.DistillationTime = normalizeHHMM(r.FormValue("distillation_time"), st.DistillationTime)
	st.TranscriptWindowN = n
	st.RetrievalK = k
	st.DistillNotify = r.FormValue("distill_notify") == "1"
	st.GroupReplyMode = pick(r.FormValue("group_reply_mode"), []string{"mention", "all"}, st.GroupReplyMode)
	// Collect the checked optional tools, validated against the catalog so only
	// known tool names can ever be enabled.
	var enabledTools []string
	for _, name := range r.Form["tool"] {
		if runtime.IsOptionalTool(name) {
			enabledTools = append(enabledTools, name)
		}
	}
	st.ExtraTools = strings.Join(enabledTools, ",")

	if err := s.db.UpdateSettings(r.Context(), st); err != nil {
		s.serverError(w, r, err)
		return
	}
	// A timezone change recomputes the user's user_local scheduled jobs.
	if oldTZ != tz {
		if err := schedule.RecomputeUserLocal(r.Context(), s.db, u.ID, time.Now()); err != nil {
			s.log.Error("recompute jobs after tz change", "user_id", u.ID, "err", err)
		}
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

func (s *Server) renderSettingsError(w http.ResponseWriter, r *http.Request, st *store.Settings, msg string) {
	data := s.settingsData(st)
	data["Flash"], data["FlashKind"] = msg, "error"
	s.render(w, r, "settings", data)
}

// pick returns v if it is in allowed, else def.
func pick(v string, allowed []string, def string) string {
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return def
}

// normalizeHHMM validates a HH:MM 24h time, returning def if invalid.
func normalizeHHMM(v, def string) string {
	if _, err := time.Parse("15:04", strings.TrimSpace(v)); err != nil {
		return def
	}
	return strings.TrimSpace(v)
}
