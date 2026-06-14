package web

import (
	"net/http"
	"strconv"
	"strings"

	"samwise/internal/orchestrator"
	"samwise/internal/store"
)

// handleAgents renders the Agents page: the user's personas, each with its own
// soul (system prompt) and optional model/runtime override (multi-agent setup).
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	agents, err := s.db.ListAgents(r.Context(), u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	active, _ := s.db.GetActiveAgent(r.Context(), u.ID)
	activeID := int64(0)
	if active != nil {
		activeID = active.ID
	}
	bots, _ := s.telegramBotViews(r.Context(), u.ID)
	data := pageData{
		"Title":        "Agents",
		"Agents":       agents,
		"ActiveID":     activeID,
		"ModelOptions": s.orch.ModelChoices(),
		"Runtimes":     s.orch.RuntimeChoices(),
		"TgBots":       bots,
		"BoxEnabled":   s.box.Enabled(),
	}
	switch r.URL.Query().Get("msg") {
	case "saved":
		data["Flash"], data["FlashKind"] = "Agent saved.", "ok"
	case "deleted":
		data["Flash"], data["FlashKind"] = "Agent deleted.", "ok"
	case "needname":
		data["Flash"], data["FlashKind"] = "An agent needs a name.", "error"
	case "paired":
		data["Flash"], data["FlashKind"] = "Telegram paired. Message your bot to start chatting.", "ok"
	case "badcode":
		data["Flash"], data["FlashKind"] = "That code is invalid or expired. Message the bot again for a fresh one.", "error"
	case "unpaired":
		data["Flash"], data["FlashKind"] = "Telegram chat unpaired.", "ok"
	case "bot_added":
		data["Flash"], data["FlashKind"] = "Telegram bot added. Message it to get a pairing code, then pair it below.", "ok"
	case "bot_saved":
		data["Flash"], data["FlashKind"] = "Telegram bot updated.", "ok"
	case "bot_deleted":
		data["Flash"], data["FlashKind"] = "Telegram bot removed.", "ok"
	case "bot_bad":
		data["Flash"], data["FlashKind"] = "Provide a label and a bot token.", "error"
	case "bot_badtoken":
		data["Flash"], data["FlashKind"] = "Telegram rejected that token (getMe failed). Check it with @BotFather.", "error"
	case "bot_nokey":
		data["Flash"], data["FlashKind"] = "Set MASTER_KEY in your env file to store bot tokens.", "error"
	}
	s.render(w, r, "agents", data)
}

// handleAgentSave creates or updates an agent.
func (s *Server) handleAgentSave(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Redirect(w, r, "/agents?msg=needname", http.StatusSeeOther)
		return
	}
	a := store.Agent{
		UserID:      u.ID,
		Name:        name,
		Description: strings.TrimSpace(r.FormValue("description")),
		Soul:        r.FormValue("soul"),
		Enabled:     true,
	}
	// Model override: resolve an alias, else accept a raw id, else "" (default).
	if id, ok := orchestrator.ResolveModel(r.FormValue("model")); ok {
		a.Model = id
	} else {
		a.Model = strings.TrimSpace(r.FormValue("model"))
	}
	// Runtime override: only a known runtime id, else "" (use the user's runtime).
	if id, ok := orchestrator.ResolveRuntime(r.FormValue("runtime")); ok {
		a.Runtime = id
	}

	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	var err error
	if id > 0 {
		a.ID = id
		err = s.db.UpdateAgent(r.Context(), a)
	} else {
		_, err = s.db.CreateAgent(r.Context(), a)
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/agents?msg=saved", http.StatusSeeOther)
}

// handleAgentSelect sets the user's active agent. Used by the Agents page and
// the chat-page selector (which passes return=/chat).
func (s *Server) handleAgentSelect(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	if id, err := strconv.ParseInt(r.FormValue("agent_id"), 10, 64); err == nil {
		if a, err := s.db.GetAgent(r.Context(), u.ID, id); err == nil {
			_ = s.db.SetActiveAgent(r.Context(), u.ID, a.ID)
		}
	}
	dest := r.FormValue("return")
	if dest != "/chat" {
		dest = "/agents"
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// handleAgentDefault makes an agent the user's default.
func (s *Server) handleAgentDefault(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	if id, err := strconv.ParseInt(r.FormValue("id"), 10, 64); err == nil {
		_ = s.db.SetDefaultAgent(r.Context(), u.ID, id)
	}
	http.Redirect(w, r, "/agents", http.StatusSeeOther)
}

// handleAgentDelete removes a non-default agent. If it was active, the active
// agent falls back to the default.
func (s *Server) handleAgentDelete(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	if id, err := strconv.ParseInt(r.FormValue("id"), 10, 64); err == nil {
		if err := s.db.DeleteAgent(r.Context(), u.ID, id); err != nil {
			s.serverError(w, r, err)
			return
		}
		if def, err := s.db.GetDefaultAgent(r.Context(), u.ID); err == nil {
			if active, _ := s.db.GetActiveAgent(r.Context(), u.ID); active == nil {
				_ = s.db.SetActiveAgent(r.Context(), u.ID, def.ID)
			}
		}
	}
	http.Redirect(w, r, "/agents?msg=deleted", http.StatusSeeOther)
}
