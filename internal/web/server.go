// Package web implements the orchestrator's HTTP portal: auth, chat, settings,
// memory editor, jobs, audit, admin, and the rendered user guide (spec §12).
//
// Server-rendered with html/template plus light JS — no SPA (spec §12). This
// file holds the Server type, route table, and the health endpoint; auth,
// settings, and chat handlers live in sibling files.
package web

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"

	"samwise/internal/config"
	"samwise/internal/orchestrator"
	"samwise/internal/secretbox"
	"samwise/internal/store"
)

// appVersion is the build version, surfaced on the admin health panel. Set via
// SetVersion at startup.
var appVersion = "dev"

// SetVersion records the build version for display in the portal.
func SetVersion(v string) { appVersion = v }

// Server holds the portal's dependencies. Construct with New.
type Server struct {
	cfg          *config.Config
	db           *store.DB
	log          *slog.Logger
	box          *secretbox.Box
	orch         *orchestrator.Orchestrator
	loginLimiter *rateLimiter
}

// New constructs a portal Server.
func New(cfg *config.Config, db *store.DB, log *slog.Logger, box *secretbox.Box, orch *orchestrator.Orchestrator) *Server {
	return &Server{
		cfg:          cfg,
		db:           db,
		log:          log,
		box:          box,
		orch:         orch,
		loginLimiter: newRateLimiter(8, time.Minute), // 8 login attempts / IP / minute
	}
}

// Handler builds the HTTP route table. Go 1.22+ method-aware patterns keep
// routing in the stdlib (spec §12: no framework).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("GET /setup", s.handleSetupForm)
	mux.HandleFunc("POST /setup", s.handleSetup)
	mux.HandleFunc("POST /logout", s.handleLogout)

	// Authenticated
	mux.HandleFunc("GET /onboarding", s.requireAuth(s.handleOnboarding))
	mux.HandleFunc("POST /onboarding", s.requireAuth(s.handleOnboardingSave))
	mux.HandleFunc("POST /onboarding/skip", s.requireAuth(s.handleOnboardingSkip))
	mux.HandleFunc("POST /onboarding/reset", s.requireAuth(s.handleOnboardingReset))
	mux.HandleFunc("GET /settings", s.requireAuth(s.handleSettings))
	mux.HandleFunc("POST /settings", s.requireAuth(s.handleSaveSettings))
	mux.HandleFunc("POST /settings/password", s.requireAuth(s.handleChangePassword))
	mux.HandleFunc("GET /chat", s.requireAuth(s.handleChat))
	mux.HandleFunc("POST /chat/send", s.requireAuth(s.handleChatSend))
	mux.HandleFunc("GET /memory", s.requireAuth(s.handleMemory))
	mux.HandleFunc("POST /memory/add", s.requireAuth(s.handleMemoryAdd))
	mux.HandleFunc("POST /memory/delete", s.requireAuth(s.handleMemoryDelete))
	mux.HandleFunc("GET /jobs", s.requireAuth(s.handleJobs))
	mux.HandleFunc("POST /jobs/create", s.requireAuth(s.handleJobCreate))
	mux.HandleFunc("POST /jobs/update", s.requireAuth(s.handleJobUpdate))
	mux.HandleFunc("POST /jobs/delete", s.requireAuth(s.handleJobDelete))
	mux.HandleFunc("GET /audit", s.requireAuth(s.handleAudit))
	mux.HandleFunc("GET /guide", s.requireAuth(s.handleGuide))
	mux.HandleFunc("GET /agents", s.requireAuth(s.handleAgents))
	mux.HandleFunc("POST /agents/save", s.requireAuth(s.handleAgentSave))
	mux.HandleFunc("POST /agents/select", s.requireAuth(s.handleAgentSelect))
	mux.HandleFunc("POST /agents/default", s.requireAuth(s.handleAgentDefault))
	mux.HandleFunc("POST /agents/delete", s.requireAuth(s.handleAgentDelete))
	mux.HandleFunc("POST /agents/telegram/add", s.requireAuth(s.handleTelegramBotAdd))
	mux.HandleFunc("POST /agents/telegram/update", s.requireAuth(s.handleTelegramBotUpdate))
	mux.HandleFunc("POST /agents/telegram/delete", s.requireAuth(s.handleTelegramBotDelete))
	mux.HandleFunc("POST /agents/telegram/pair", s.requireAuth(s.handlePairingSubmit))
	mux.HandleFunc("POST /agents/telegram/unpair", s.requireAuth(s.handleTelegramUnpair))
	mux.HandleFunc("GET /extensions", s.requireAuth(s.handleExtensions))
	mux.HandleFunc("POST /extensions/mcp/add", s.requireAuth(s.handleMCPAdd))
	mux.HandleFunc("POST /extensions/mcp/toggle", s.requireAuth(s.handleMCPToggle))
	mux.HandleFunc("POST /extensions/mcp/delete", s.requireAuth(s.handleMCPDelete))
	mux.HandleFunc("POST /extensions/skills/save", s.requireAuth(s.handleSkillSave))
	mux.HandleFunc("POST /extensions/skills/import", s.requireAuth(s.handleSkillImport))
	mux.HandleFunc("POST /extensions/skills/toggle", s.requireAuth(s.handleSkillToggle))
	mux.HandleFunc("POST /extensions/skills/delete", s.requireAuth(s.handleSkillDelete))
	mux.HandleFunc("GET /extensions/skills/file", s.requireAuth(s.handleSkillFile))
	mux.HandleFunc("POST /extensions/secrets/add", s.requireAuth(s.handleSecretAdd))
	mux.HandleFunc("POST /extensions/secrets/delete", s.requireAuth(s.handleSecretDelete))
	mux.HandleFunc("POST /extensions/secrets/refresh", s.requireAuth(s.handleSecretRefresh))

	// Admin
	mux.HandleFunc("GET /admin", s.requireAdmin(s.handleAdmin))
	mux.HandleFunc("POST /admin/users/create", s.requireAdmin(s.handleAdminCreateUser))
	mux.HandleFunc("POST /admin/users/toggle", s.requireAdmin(s.handleAdminToggleUser))
	mux.HandleFunc("POST /admin/users/password", s.requireAdmin(s.handleAdminResetPassword))

	return mux
}

// clientIP extracts a best-effort client IP for rate-limiting keys.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// handleHealth is the uptime/monitoring endpoint (spec §11). It verifies DB
// connectivity so a crash-looping DB surfaces as an unhealthy status.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	code := http.StatusOK
	if err := s.db.PingContext(r.Context()); err != nil {
		status = "db_error"
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]string{"status": status})
}

// handleIndex routes the root to the right place: the chat page when logged in,
// the first-run setup when no accounts exist, otherwise the login page.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if _, ok := s.readSession(r); ok {
		http.Redirect(w, r, "/chat", http.StatusSeeOther)
		return
	}
	if n, err := s.db.CountUsers(r.Context()); err == nil && n == 0 {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
