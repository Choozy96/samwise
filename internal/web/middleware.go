package web

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"samwise/internal/store"
)

type ctxKey int

const userCtxKey ctxKey = iota

// requireAuth wraps a handler so it only runs for a logged-in, non-disabled
// user. The user is loaded once and stashed in the request context. Unauthenticated
// requests are redirected to /login.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := s.readSession(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		u, err := s.db.GetUserByID(r.Context(), uid)
		if err != nil || u.Disabled {
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				s.log.Error("loading session user", "err", err)
			}
			s.clearSession(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), userCtxKey, u)

		// First-run gate: send un-onboarded users to the setup wizard. Only on
		// page (GET) requests, and never for the wizard itself or logout.
		if r.Method == http.MethodGet && !strings.HasPrefix(r.URL.Path, "/onboarding") && r.URL.Path != "/logout" {
			if done, err := s.db.GetOnboarded(ctx, u.ID); err == nil && !done {
				http.Redirect(w, r, "/onboarding", http.StatusSeeOther)
				return
			}
		}
		next(w, r.WithContext(ctx))
	}
}

// requireAdmin is requireAuth plus an is_admin check (spec §9).
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if u := currentUser(r.Context()); u == nil || !u.IsAdmin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	})
}

// currentUser returns the authenticated user from the request context, or nil.
func currentUser(ctx context.Context) *store.User {
	u, _ := ctx.Value(userCtxKey).(*store.User)
	return u
}
