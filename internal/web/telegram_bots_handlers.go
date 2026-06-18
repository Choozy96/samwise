package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"samwise/internal/store"
	"samwise/internal/telegram"
)

// botView is a telegram_bots row decorated for the Agents page: its bound agent's
// name (if any) and whether the user has paired a chat to it. Legacy marks the
// single bot configured via the environment (bot_id 0) — it has no DB row, so it
// can be paired/unpaired but not edited, deleted, or agent-bound here.
type botView struct {
	store.TelegramBot
	AgentName string
	Pairings  []store.ChannelIdentity // every chat paired to this bot (a bot can have many)
	Legacy    bool
}

// telegramBotViews loads the user's bots with their bound-agent names and pairing
// status, plus the user's agents (for the bind dropdown). When the deployment has
// a legacy env-token bot, it's prepended as a read-only row so the user can see
// and unpair it.
func (s *Server) telegramBotViews(ctx context.Context, userID int64) (views []botView, agents []store.Agent) {
	bots, _ := s.db.ListTelegramBots(ctx, userID)
	agents, _ = s.db.ListAgents(ctx, userID)
	nameByID := map[int64]string{}
	for _, a := range agents {
		nameByID[a.ID] = a.Name
	}
	// A bot can have several chats paired (different Telegram senders), so collect
	// all of them per bot, not just one.
	pairingsByBot := map[int64][]store.ChannelIdentity{}
	if idents, err := s.db.ListIdentitiesByUser(ctx, userID, "telegram"); err == nil {
		for _, id := range idents {
			pairingsByBot[id.BotID] = append(pairingsByBot[id.BotID], id)
		}
	}
	// Show the legacy env bot (bot_id 0) when it's configured, OR when an existing
	// bot_id-0 pairing would otherwise be orphaned (env token removed after pairing).
	if s.cfg.TelegramBotToken != "" || len(pairingsByBot[0]) > 0 {
		views = append(views, botView{
			TelegramBot: store.TelegramBot{ID: 0, Label: "Default bot (from environment)", Enabled: true},
			Pairings:    pairingsByBot[0], Legacy: true,
		})
	}
	for _, b := range bots {
		views = append(views, botView{
			TelegramBot: b, AgentName: nameByID[b.AgentID], Pairings: pairingsByBot[b.ID],
		})
	}
	return views, agents
}

// handleTelegramUnpair removes one paired chat from a bot (bot id 0 = the legacy
// env bot), so a fresh code can be redeemed later. Other chats on the same bot
// are left paired.
func (s *Server) handleTelegramUnpair(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	botID, _ := strconv.ParseInt(r.FormValue("bot_id"), 10, 64)
	externalID := strings.TrimSpace(r.FormValue("external_id"))
	if externalID == "" {
		http.Redirect(w, r, "/agents", http.StatusSeeOther)
		return
	}
	if err := s.db.DeleteIdentity(r.Context(), u.ID, "telegram", botID, externalID); err != nil {
		s.serverError(w, r, err)
		return
	}
	_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "channel", "telegram_unpair",
		"bot "+strconv.FormatInt(botID, 10)+" chat "+externalID, "ok")
	http.Redirect(w, r, "/agents?msg=unpaired", http.StatusSeeOther)
}

// handleTelegramBotAdd registers a per-user bot: validates the token via getMe,
// caches the @username, and stores the token encrypted at rest.
func (s *Server) handleTelegramBotAdd(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	if !s.box.Enabled() {
		http.Redirect(w, r, "/agents?msg=bot_nokey", http.StatusSeeOther)
		return
	}
	label := strings.TrimSpace(r.FormValue("label"))
	token := strings.TrimSpace(r.FormValue("token"))
	if label == "" || token == "" {
		http.Redirect(w, r, "/agents?msg=bot_bad", http.StatusSeeOther)
		return
	}
	agentID := s.validAgentID(r.Context(), u.ID, r.FormValue("agent_id"))

	// Validate the token now so the user gets immediate feedback, and cache the
	// bot's @username for display.
	vctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	_, username, err := telegram.NewClient(token).GetMe(vctx)
	cancel()
	if err != nil {
		http.Redirect(w, r, "/agents?msg=bot_badtoken", http.StatusSeeOther)
		return
	}

	enc, err := s.box.Encrypt([]byte(token))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if _, err := s.db.CreateTelegramBot(r.Context(), store.TelegramBot{
		UserID: u.ID, Label: label, TokenEnc: enc, Username: username, AgentID: agentID, Enabled: true,
	}); err != nil {
		s.serverError(w, r, err)
		return
	}
	_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "channel", "telegram_bot_add", "@"+username, "ok")
	http.Redirect(w, r, "/agents?msg=bot_added", http.StatusSeeOther)
}

// handleTelegramBotUpdate edits a bot's label, bound agent, and enabled flag, and
// optionally replaces its token.
func (s *Server) handleTelegramBotUpdate(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	bot, err := s.db.GetTelegramBot(r.Context(), u.ID, id)
	if err != nil {
		http.Redirect(w, r, "/agents", http.StatusSeeOther)
		return
	}
	label := strings.TrimSpace(r.FormValue("label"))
	if label == "" {
		label = bot.Label
	}
	agentID := s.validAgentID(r.Context(), u.ID, r.FormValue("agent_id"))
	enabled := r.FormValue("enabled") == "1"
	if err := s.db.UpdateTelegramBot(r.Context(), u.ID, id, label, agentID, enabled); err != nil {
		s.serverError(w, r, err)
		return
	}

	// Optional token replacement (validate + re-cache username).
	if token := strings.TrimSpace(r.FormValue("token")); token != "" {
		if !s.box.Enabled() {
			http.Redirect(w, r, "/agents?msg=bot_nokey", http.StatusSeeOther)
			return
		}
		vctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		_, username, gerr := telegram.NewClient(token).GetMe(vctx)
		cancel()
		if gerr != nil {
			http.Redirect(w, r, "/agents?msg=bot_badtoken", http.StatusSeeOther)
			return
		}
		enc, eerr := s.box.Encrypt([]byte(token))
		if eerr != nil {
			s.serverError(w, r, eerr)
			return
		}
		if uerr := s.db.UpdateTelegramBotToken(r.Context(), u.ID, id, enc); uerr != nil {
			s.serverError(w, r, uerr)
			return
		}
		_ = s.db.SetTelegramBotUsername(r.Context(), id, username)
	}
	http.Redirect(w, r, "/agents?msg=bot_saved", http.StatusSeeOther)
}

// handleTelegramBotDelete removes a bot and its pairings.
func (s *Server) handleTelegramBotDelete(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err := s.db.DeleteTelegramBot(r.Context(), u.ID, id); err != nil {
		s.serverError(w, r, err)
		return
	}
	_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "channel", "telegram_bot_delete", strconv.FormatInt(id, 10), "ok")
	http.Redirect(w, r, "/agents?msg=bot_deleted", http.StatusSeeOther)
}

// validAgentID parses an agent id form value and returns it only if it's one of
// the user's agents (0 = unbound).
func (s *Server) validAgentID(ctx context.Context, userID int64, raw string) int64 {
	id, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if id == 0 {
		return 0
	}
	if a, err := s.db.GetAgent(ctx, userID, id); err == nil && a != nil {
		return a.ID
	}
	return 0
}
