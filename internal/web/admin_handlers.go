package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"samwise/internal/auth"
	"samwise/internal/store"
)

// handleAdmin renders the admin dashboard: users + system health.
func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	users, err := s.db.ListUsers(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	dbStatus := "ok"
	if err := s.db.PingContext(r.Context()); err != nil {
		dbStatus = "error"
	}
	mcpAddr, mcpAlive := s.orch.MCPStatus()
	mcpStatus := "ok (" + mcpAddr + ")"
	if !mcpAlive {
		mcpStatus = "DOWN — agent runs have no core tools"
	}
	health := map[string]any{
		"DB":        dbStatus,
		"DBPath":    s.cfg.DBPath,
		"UserCount": len(users),
		"Version":   appVersion,
		"MCPHost":   mcpStatus,
	}
	models, err := s.db.ListModels(r.Context(), false) // all, incl. disabled, for admin
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	u := currentUser(r.Context())
	anchor, _ := s.db.GetSystemSetting(r.Context(), store.AnchorChatKey)
	data := pageData{
		"Title": "Admin", "Users": users, "Health": health, "Models": models,
		"AnchorChat": anchor,
		// The anchor is picked from chats the ADMIN is paired to.
		"AnchorTargets": s.deliveryTargets(r, u.ID),
	}
	s.addUsageData(r, data)
	switch r.URL.Query().Get("msg") {
	case "created":
		data["Flash"], data["FlashKind"] = "User created.", "ok"
	case "exists":
		data["Flash"], data["FlashKind"] = "That username is taken.", "error"
	case "invalid":
		data["Flash"], data["FlashKind"] = "Username must be ≥3 chars and password ≥8.", "error"
	case "pwreset":
		data["Flash"], data["FlashKind"] = "Password reset — give the new password to the user.", "ok"
	case "pwshort":
		data["Flash"], data["FlashKind"] = "New password must be at least 8 characters.", "error"
	case "noadminreset":
		data["Flash"], data["FlashKind"] = "Admins change their own password in Settings (not here).", "error"
	case "modelsaved":
		data["Flash"], data["FlashKind"] = "Model saved.", "ok"
	case "modeldeleted":
		data["Flash"], data["FlashKind"] = "Model removed.", "ok"
	case "modelbad":
		data["Flash"], data["FlashKind"] = "A model needs a label. Alias must be unique.", "error"
	case "anchorsaved":
		data["Flash"], data["FlashKind"] = "Anchor chat saved — scheduled results now land there by default.", "ok"
	case "anchorcleared":
		data["Flash"], data["FlashKind"] = "Anchor chat cleared.", "ok"
	case "anchorbad":
		data["Flash"], data["FlashKind"] = "Pick one of your paired chats as the anchor.", "error"
	}
	s.render(w, r, "admin", data)
}

// handleAdminAnchor sets or clears the system-wide anchor (announcement) chat.
// The value is validated against the admin's own paired chats.
func (s *Server) handleAdminAnchor(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	v := strings.TrimSpace(r.FormValue("anchor"))
	if v != "" {
		ok := false
		for _, t := range s.deliveryTargets(r, u.ID) {
			if t.Value == v {
				ok = true
				break
			}
		}
		if !ok {
			http.Redirect(w, r, "/admin?msg=anchorbad", http.StatusSeeOther)
			return
		}
	}
	if err := s.db.SetSystemSetting(r.Context(), store.AnchorChatKey, v); err != nil {
		s.serverError(w, r, err)
		return
	}
	_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "admin", "anchor_chat", v, "ok")
	msg := "anchorsaved"
	if v == "" {
		msg = "anchorcleared"
	}
	http.Redirect(w, r, "/admin?msg="+msg, http.StatusSeeOther)
}

// addUsageData assembles the admin usage panel: token/cost totals over a period
// (quick 1d/7d/30d or a custom UTC date range), grouped by user, model, both, or
// nothing (one total row).
func (s *Server) addUsageData(r *http.Request, data pageData) {
	const f = "2006-01-02 15:04:05"
	q := r.URL.Query()

	period := q.Get("up")
	group := q.Get("ug")
	switch group {
	case "user", "model", "user_model", "":
	default:
		group = "user"
	}

	now := time.Now().UTC()
	from, to := "", ""
	switch period {
	case "1d":
		from = now.Add(-24 * time.Hour).Format(f)
	case "30d":
		from = now.Add(-30 * 24 * time.Hour).Format(f)
	case "custom":
		// Dates are interpreted as UTC calendar days, [from, to+1d).
		if t, err := time.Parse("2006-01-02", q.Get("ufrom")); err == nil {
			from = t.Format(f)
		}
		if t, err := time.Parse("2006-01-02", q.Get("uto")); err == nil {
			to = t.AddDate(0, 0, 1).Format(f)
		}
		if from == "" {
			period = "7d"
			from = now.Add(-7 * 24 * time.Hour).Format(f)
		}
	default:
		period = "7d"
		from = now.Add(-7 * 24 * time.Hour).Format(f)
	}

	rows, err := s.db.UsageReport(r.Context(), from, to, group)
	if err != nil {
		s.log.Error("admin usage report", "err", err)
		return
	}
	var total store.UsageRow
	for _, u := range rows {
		total.Runs += u.Runs
		total.InputTokens += u.InputTokens
		total.OutputTokens += u.OutputTokens
		total.CacheWrite += u.CacheWrite
		total.CacheRead += u.CacheRead
		total.CostUSD += u.CostUSD
	}
	data["Usage"] = rows
	data["UsageTotal"] = total
	data["UsagePeriod"] = period
	data["UsageGroup"] = group
	data["UsageFrom"] = q.Get("ufrom")
	data["UsageTo"] = q.Get("uto")
}

// handleAdminModelSave adds a new model to the catalog or updates an existing one
// (id present). Label is required; alias and model_id are optional (a blank
// model_id means the runtime's own default).
func (s *Server) handleAdminModelSave(w http.ResponseWriter, r *http.Request) {
	label := strings.TrimSpace(r.FormValue("label"))
	if label == "" {
		http.Redirect(w, r, "/admin?msg=modelbad", http.StatusSeeOther)
		return
	}
	// NOTE: an unchecked checkbox submits nothing, so empty means DISABLED — the
	// add form's checkbox is pre-checked, so a default add still arrives enabled.
	m := store.Model{
		Alias:   strings.ToLower(strings.TrimSpace(r.FormValue("alias"))),
		ModelID: strings.TrimSpace(r.FormValue("model_id")),
		Label:   label,
		Enabled: r.FormValue("enabled") == "1" || r.FormValue("enabled") == "on",
	}
	m.Sort, _ = strconv.Atoi(r.FormValue("sort"))

	var err error
	if idStr := r.FormValue("id"); idStr != "" {
		m.ID, _ = strconv.ParseInt(idStr, 10, 64)
		err = s.db.UpdateModel(r.Context(), m)
	} else {
		_, err = s.db.CreateModel(r.Context(), m)
	}
	if err != nil {
		// Most likely a duplicate alias (unique index) — treat as a validation error.
		http.Redirect(w, r, "/admin?msg=modelbad", http.StatusSeeOther)
		return
	}
	u := currentUser(r.Context())
	_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "admin", "model_save", label, "ok")
	http.Redirect(w, r, "/admin?msg=modelsaved", http.StatusSeeOther)
}

// handleAdminModelDelete removes a model from the catalog. Users who had it
// selected keep the raw id in their settings (it just loses its friendly label).
func (s *Server) handleAdminModelDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	if err := s.db.DeleteModel(r.Context(), id); err != nil {
		s.serverError(w, r, err)
		return
	}
	u := currentUser(r.Context())
	_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "admin", "model_delete", strconv.FormatInt(id, 10), "ok")
	http.Redirect(w, r, "/admin?msg=modeldeleted", http.StatusSeeOther)
}

// handleAdminResetPassword sets a new password for a non-admin user — recovery
// for when a user forgets theirs. Admin accounts manage their own password via
// Settings (or the set-password CLI), so this won't touch them.
func (s *Server) handleAdminResetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	password := r.FormValue("password")
	if len(password) < 8 {
		http.Redirect(w, r, "/admin?msg=pwshort", http.StatusSeeOther)
		return
	}
	target, err := s.db.GetUserByID(r.Context(), id)
	if err != nil {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	if target.IsAdmin {
		http.Redirect(w, r, "/admin?msg=noadminreset", http.StatusSeeOther)
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.db.UpdatePassword(r.Context(), id, hash); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("admin reset password", "user_id", id, "username", target.Username)
	_ = s.db.AddAuditEvent(r.Context(), id, 0, "auth", "password_reset", "by admin", "ok")
	http.Redirect(w, r, "/admin?msg=pwreset", http.StatusSeeOther)
}

// handleAdminCreateUser creates a standard (non-admin) user.
func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	if validateNewCredentials(username, password) != "" {
		http.Redirect(w, r, "/admin?msg=invalid", http.StatusSeeOther)
		return
	}
	if existing, _ := s.db.GetUserByUsername(r.Context(), username); existing != nil {
		http.Redirect(w, r, "/admin?msg=exists", http.StatusSeeOther)
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if _, err := s.db.CreateUser(r.Context(), username, hash, false); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("admin created user", "username", username)
	http.Redirect(w, r, "/admin?msg=created", http.StatusSeeOther)
}

// handleAdminToggleUser enables/disables a non-admin account.
func (s *Server) handleAdminToggleUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	target, err := s.db.GetUserByID(r.Context(), id)
	if err != nil {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	if target.IsAdmin {
		// Never disable an admin via this control.
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	disabled := r.FormValue("disabled") == "1"
	if err := s.db.SetUserDisabled(r.Context(), id, disabled); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}
