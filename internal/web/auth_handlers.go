package web

import (
	"errors"
	"net/http"
	"strings"

	"samwise/internal/auth"
	"samwise/internal/store"
)

// handleLoginForm renders the sign-in page. If no accounts exist yet, it sends
// the visitor to first-run setup instead.
func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if n, err := s.db.CountUsers(r.Context()); err == nil && n == 0 {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if _, ok := s.readSession(r); ok {
		http.Redirect(w, r, "/chat", http.StatusSeeOther)
		return
	}
	s.render(w, r, "login", pageData{"Title": "Sign in"})
}

// handleLogin verifies credentials and issues a session. Failures are rate
// limited per IP and reported with a generic message (no username enumeration).
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.loginLimiter.allow(s.clientIP(r)) {
		s.render(w, r, "login", pageData{"Title": "Sign in", "Flash": "Too many attempts. Wait a minute and try again.", "FlashKind": "error"})
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	u, err := s.db.GetUserByUsername(r.Context(), username)
	if err != nil || u.Disabled || auth.VerifyPassword(password, u.PasswordHash) != nil {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			s.log.Error("login lookup", "err", err)
		}
		// Audit a failed attempt against a real account (helps spot misuse).
		if u != nil {
			_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "auth", "login_failed", "web ("+s.clientIP(r)+")", "denied")
		}
		s.render(w, r, "login", pageData{"Title": "Sign in", "Flash": "Invalid username or password.", "FlashKind": "error"})
		return
	}
	s.issueSession(w, u.ID)
	s.log.Info("login", "user_id", u.ID, "username", u.Username)
	_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "auth", "login", "web ("+s.clientIP(r)+")", "ok")
	http.Redirect(w, r, "/chat", http.StatusSeeOther)
}

// handleSetupForm renders first-run admin creation, but only while there are no
// accounts. Once an admin exists, further users are created from the admin page.
func (s *Server) handleSetupForm(w http.ResponseWriter, r *http.Request) {
	if n, err := s.db.CountUsers(r.Context()); err != nil || n > 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, r, "login", pageData{"Title": "Setup", "FirstRun": true})
}

// handleSetup creates the first account as admin.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	n, err := s.db.CountUsers(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if n > 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	password2 := r.FormValue("password2")

	if fail := validateNewCredentials(username, password); fail != "" {
		s.render(w, r, "login", pageData{"Title": "Setup", "FirstRun": true, "Flash": fail, "FlashKind": "error"})
		return
	}
	if password != password2 {
		s.render(w, r, "login", pageData{"Title": "Setup", "FirstRun": true, "Flash": "Passwords do not match.", "FlashKind": "error"})
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	id, err := s.db.CreateUser(r.Context(), username, hash, true)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.issueSession(w, id)
	s.log.Info("admin account created", "user_id", id, "username", username)
	_ = s.db.AddAuditEvent(r.Context(), id, 0, "auth", "login", "first admin account · web ("+s.clientIP(r)+")", "ok")
	http.Redirect(w, r, "/chat", http.StatusSeeOther)
}

// handleLogout clears the session.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.clearSession(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// validateNewCredentials returns a user-facing error string, or "" if valid.
func validateNewCredentials(username, password string) string {
	if len(username) < 3 {
		return "Username must be at least 3 characters."
	}
	if len(password) < 8 {
		return "Password must be at least 8 characters."
	}
	return ""
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("server error", "path", r.URL.Path, "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
