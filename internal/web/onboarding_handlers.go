package web

import (
	"net/http"
	"strings"
	"time"

	"samwise/internal/orchestrator"
	"samwise/internal/schedule"
)

// handleOnboarding renders the first-run setup wizard.
func (s *Server) handleOnboarding(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	settings, err := s.db.GetSettings(r.Context(), u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	agent, _ := s.db.GetDefaultAgent(r.Context(), u.ID)
	s.render(w, r, "onboarding", pageData{
		"Title": "Welcome",
		"S":     settings,
		"Agent": agent,
	})
}

// handleOnboardingSave applies the wizard: seeds memory, personalizes the
// default agent, sets timezone/delivery, optionally sets up the briefing, and
// marks onboarding complete.
func (s *Server) handleOnboardingSave(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	ctx := r.Context()
	settings, err := s.db.GetSettings(ctx, u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	// Memory: replace any prior onboarding-sourced entries, then seed fresh.
	_ = s.db.DeleteSemanticBySource(ctx, u.ID, "onboarding")
	if v := strings.TrimSpace(r.FormValue("call_name")); v != "" {
		_, _ = s.db.SaveSemantic(ctx, u.ID, 0, "profile", "preference", "Address the user as "+v+".", "onboarding")
	}
	if v := strings.TrimSpace(r.FormValue("about")); v != "" {
		_, _ = s.db.SaveSemantic(ctx, u.ID, 0, "about-me", "fact", v, "onboarding")
	}
	if v := strings.TrimSpace(r.FormValue("style")); v != "" {
		_, _ = s.db.SaveSemantic(ctx, u.ID, 0, "style", "preference", v, "onboarding")
	}

	// Default agent: optional rename + persona (composed with operational rules).
	if def, err := s.db.GetDefaultAgent(ctx, u.ID); err == nil {
		changed := false
		if name := strings.TrimSpace(r.FormValue("agent_name")); name != "" && name != def.Name {
			def.Name = name
			changed = true
		}
		if persona := strings.TrimSpace(r.FormValue("persona")); persona != "" {
			def.Soul = orchestrator.ComposeSoul(persona)
			changed = true
		}
		if changed {
			_ = s.db.UpdateAgent(ctx, *def)
		}
	}

	// Settings: timezone + delivery channel.
	oldTZ := settings.Timezone
	if tz := strings.TrimSpace(r.FormValue("timezone")); tz != "" {
		if _, err := time.LoadLocation(tz); err == nil {
			settings.Timezone = tz
		}
	}
	settings.DeliveryChannel = pick(r.FormValue("delivery"), []string{"web", "telegram"}, settings.DeliveryChannel)
	if err := s.db.UpdateSettings(ctx, settings); err != nil {
		s.serverError(w, r, err)
		return
	}
	if oldTZ != settings.Timezone {
		if err := schedule.RecomputeUserLocal(ctx, s.db, u.ID, time.Now()); err != nil {
			s.log.Error("recompute jobs after onboarding tz", "err", err)
		}
	}

	// Optional morning briefing.
	if r.FormValue("briefing") == "1" {
		if _, err := s.ensureMorningBriefing(ctx, u.ID, settings); err != nil {
			s.log.Error("onboarding briefing setup", "err", err)
		}
	}

	_ = s.db.SetOnboarded(ctx, u.ID, true)
	_ = s.db.AddAuditEvent(ctx, u.ID, 0, "auth", "onboarding", "completed first-run setup", "ok")
	http.Redirect(w, r, "/chat", http.StatusSeeOther)
}

// handleOnboardingSkip marks onboarding complete without changes.
func (s *Server) handleOnboardingSkip(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	_ = s.db.SetOnboarded(r.Context(), u.ID, true)
	http.Redirect(w, r, "/chat", http.StatusSeeOther)
}

// handleOnboardingReset re-triggers the wizard (from Settings).
func (s *Server) handleOnboardingReset(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	_ = s.db.SetOnboarded(r.Context(), u.ID, false)
	http.Redirect(w, r, "/onboarding", http.StatusSeeOther)
}
