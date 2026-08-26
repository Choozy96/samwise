package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"samwise/internal/discord"
	"samwise/internal/store"
)

// discordBotView is a discord_bots row decorated for the Agents page.
type discordBotView struct {
	store.DiscordBot
	AgentName string
	Pairings  []store.ChannelIdentity
}

// discordBotViews loads the user's Discord bots with bound-agent names and
// pairings.
func (s *Server) discordBotViews(ctx context.Context, userID int64) []discordBotView {
	bots, _ := s.db.ListDiscordBots(ctx, userID)
	agents, _ := s.db.ListAgents(ctx, userID)
	nameByID := map[int64]string{}
	for _, a := range agents {
		nameByID[a.ID] = a.Name
	}
	pairingsByBot := map[int64][]store.ChannelIdentity{}
	if idents, err := s.db.ListIdentitiesByUser(ctx, userID, "discord"); err == nil {
		for _, id := range idents {
			pairingsByBot[id.BotID] = append(pairingsByBot[id.BotID], id)
		}
	}
	var views []discordBotView
	for _, b := range bots {
		views = append(views, discordBotView{
			DiscordBot: b, AgentName: nameByID[b.AgentID], Pairings: pairingsByBot[b.ID],
		})
	}
	return views
}

// handleDiscordBotAdd registers a Discord bot: validates the token via the REST
// API, caches the bot identity, stores the token encrypted.
func (s *Server) handleDiscordBotAdd(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	if !s.box.Enabled() {
		http.Redirect(w, r, "/agents?msg=bot_nokey", http.StatusSeeOther)
		return
	}
	label := strings.TrimSpace(r.FormValue("label"))
	token := strings.TrimSpace(r.FormValue("token"))
	if label == "" || token == "" {
		http.Redirect(w, r, "/agents?msg=discord_bad", http.StatusSeeOther)
		return
	}
	agentID := s.validAgentID(r.Context(), u.ID, r.FormValue("agent_id"))

	vctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	botUserID, username, err := discord.ValidateToken(vctx, token)
	cancel()
	if err != nil {
		http.Redirect(w, r, "/agents?msg=discord_badtoken", http.StatusSeeOther)
		return
	}

	enc, err := s.box.Encrypt([]byte(token))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if _, err := s.db.CreateDiscordBot(r.Context(), store.DiscordBot{
		UserID: u.ID, Label: label, TokenEnc: enc,
		BotUserID: botUserID, Username: username, AgentID: agentID, Enabled: true,
	}); err != nil {
		s.serverError(w, r, err)
		return
	}
	_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "channel", "discord_bot_add", username, "ok")
	http.Redirect(w, r, "/agents?msg=discord_added", http.StatusSeeOther)
}

// handleDiscordBotUpdate edits a bot's label, bound agent, and enabled flag,
// and optionally replaces its token.
func (s *Server) handleDiscordBotUpdate(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	bot, err := s.db.GetDiscordBot(r.Context(), u.ID, id)
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
	if err := s.db.UpdateDiscordBot(r.Context(), u.ID, id, label, agentID, enabled); err != nil {
		s.serverError(w, r, err)
		return
	}

	if token := strings.TrimSpace(r.FormValue("token")); token != "" {
		if !s.box.Enabled() {
			http.Redirect(w, r, "/agents?msg=bot_nokey", http.StatusSeeOther)
			return
		}
		vctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		botUserID, username, gerr := discord.ValidateToken(vctx, token)
		cancel()
		if gerr != nil {
			http.Redirect(w, r, "/agents?msg=discord_badtoken", http.StatusSeeOther)
			return
		}
		enc, eerr := s.box.Encrypt([]byte(token))
		if eerr != nil {
			s.serverError(w, r, eerr)
			return
		}
		if uerr := s.db.UpdateDiscordBotToken(r.Context(), u.ID, id, enc); uerr != nil {
			s.serverError(w, r, uerr)
			return
		}
		_ = s.db.SetDiscordBotIdentity(r.Context(), id, botUserID, username)
	}
	http.Redirect(w, r, "/agents?msg=discord_saved", http.StatusSeeOther)
}

// handleDiscordBotDelete removes a bot and its pairings.
func (s *Server) handleDiscordBotDelete(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err := s.db.DeleteDiscordBot(r.Context(), u.ID, id); err != nil {
		s.serverError(w, r, err)
		return
	}
	_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "channel", "discord_bot_delete", strconv.FormatInt(id, 10), "ok")
	http.Redirect(w, r, "/agents?msg=discord_deleted", http.StatusSeeOther)
}

// handleDiscordUnpair removes one paired chat from a bot.
func (s *Server) handleDiscordUnpair(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	botID, _ := strconv.ParseInt(r.FormValue("bot_id"), 10, 64)
	externalID := strings.TrimSpace(r.FormValue("external_id"))
	if externalID == "" {
		http.Redirect(w, r, "/agents", http.StatusSeeOther)
		return
	}
	if err := s.db.DeleteIdentity(r.Context(), u.ID, "discord", botID, externalID); err != nil {
		s.serverError(w, r, err)
		return
	}
	_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "channel", "discord_unpair",
		"bot "+strconv.FormatInt(botID, 10)+" chat "+externalID, "ok")
	http.Redirect(w, r, "/agents?msg=unpaired", http.StatusSeeOther)
}
