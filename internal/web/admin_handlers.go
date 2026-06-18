package web

import (
	"net/http"
	"strconv"
	"strings"

	"samwise/internal/auth"
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
	health := map[string]any{
		"DB":        dbStatus,
		"DBPath":    s.cfg.DBPath,
		"UserCount": len(users),
		"Version":   appVersion,
	}
	data := pageData{"Title": "Admin", "Users": users, "Health": health}
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
	}
	s.render(w, r, "admin", data)
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
