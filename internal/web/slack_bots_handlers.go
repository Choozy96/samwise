package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	slackch "samwise/internal/slack"
	"samwise/internal/store"
)

// slackBotView is a slack_bots row decorated for the Agents page: its bound
// agent's name and the chats paired to it.
type slackBotView struct {
	store.SlackBot
	AgentName string
	Pairings  []store.ChannelIdentity
}

// slackBotViews loads the user's Slack apps with bound-agent names and pairings.
func (s *Server) slackBotViews(ctx context.Context, userID int64) []slackBotView {
	bots, _ := s.db.ListSlackBots(ctx, userID)
	agents, _ := s.db.ListAgents(ctx, userID)
	nameByID := map[int64]string{}
	for _, a := range agents {
		nameByID[a.ID] = a.Name
	}
	pairingsByBot := map[int64][]store.ChannelIdentity{}
	if idents, err := s.db.ListIdentitiesByUser(ctx, userID, "slack"); err == nil {
		for _, id := range idents {
			pairingsByBot[id.BotID] = append(pairingsByBot[id.BotID], id)
		}
	}
	var views []slackBotView
	for _, b := range bots {
		views = append(views, slackBotView{
			SlackBot: b, AgentName: nameByID[b.AgentID], Pairings: pairingsByBot[b.ID],
		})
	}
	return views
}

// handleSlackBotAdd registers a Slack app: validates the bot token via
// auth.test, caches the bot user id + workspace, stores both tokens encrypted.
func (s *Server) handleSlackBotAdd(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	if !s.box.Enabled() {
		http.Redirect(w, r, "/agents?msg=bot_nokey", http.StatusSeeOther)
		return
	}
	label := strings.TrimSpace(r.FormValue("label"))
	botToken := strings.TrimSpace(r.FormValue("bot_token"))
	appToken := strings.TrimSpace(r.FormValue("app_token"))
	if label == "" || botToken == "" || appToken == "" ||
		!strings.HasPrefix(botToken, "xoxb-") || !strings.HasPrefix(appToken, "xapp-") {
		http.Redirect(w, r, "/agents?msg=slack_bad", http.StatusSeeOther)
		return
	}
	agentID := s.validAgentID(r.Context(), u.ID, r.FormValue("agent_id"))

	vctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	botUserID, teamName, err := slackch.NewClient(botToken).AuthTest(vctx)
	cancel()
	if err != nil {
		http.Redirect(w, r, "/agents?msg=slack_badtoken", http.StatusSeeOther)
		return
	}

	botEnc, err := s.box.Encrypt([]byte(botToken))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	appEnc, err := s.box.Encrypt([]byte(appToken))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if _, err := s.db.CreateSlackBot(r.Context(), store.SlackBot{
		UserID: u.ID, Label: label, BotTokenEnc: botEnc, AppTokenEnc: appEnc,
		BotUserID: botUserID, TeamName: teamName, AgentID: agentID, Enabled: true,
	}); err != nil {
		s.serverError(w, r, err)
		return
	}
	_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "channel", "slack_bot_add", teamName, "ok")
	http.Redirect(w, r, "/agents?msg=slack_added", http.StatusSeeOther)
}

// handleSlackBotUpdate edits an app's label, bound agent, and enabled flag, and
// optionally replaces its tokens (both together).
func (s *Server) handleSlackBotUpdate(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	bot, err := s.db.GetSlackBot(r.Context(), u.ID, id)
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
	if err := s.db.UpdateSlackBot(r.Context(), u.ID, id, label, agentID, enabled); err != nil {
		s.serverError(w, r, err)
		return
	}

	botToken := strings.TrimSpace(r.FormValue("bot_token"))
	appToken := strings.TrimSpace(r.FormValue("app_token"))
	if botToken != "" || appToken != "" {
		if botToken == "" || appToken == "" {
			// The pair travels together (they belong to one Slack app version).
			http.Redirect(w, r, "/agents?msg=slack_bad", http.StatusSeeOther)
			return
		}
		if !s.box.Enabled() {
			http.Redirect(w, r, "/agents?msg=bot_nokey", http.StatusSeeOther)
			return
		}
		vctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		botUserID, teamName, gerr := slackch.NewClient(botToken).AuthTest(vctx)
		cancel()
		if gerr != nil {
			http.Redirect(w, r, "/agents?msg=slack_badtoken", http.StatusSeeOther)
			return
		}
		botEnc, e1 := s.box.Encrypt([]byte(botToken))
		appEnc, e2 := s.box.Encrypt([]byte(appToken))
		if e1 != nil || e2 != nil {
			s.serverError(w, r, e1)
			return
		}
		if uerr := s.db.UpdateSlackBotTokens(r.Context(), u.ID, id, botEnc, appEnc); uerr != nil {
			s.serverError(w, r, uerr)
			return
		}
		_ = s.db.SetSlackBotIdentity(r.Context(), id, botUserID, teamName)
	}
	http.Redirect(w, r, "/agents?msg=slack_saved", http.StatusSeeOther)
}

// handleSlackBotDelete removes an app and its pairings.
func (s *Server) handleSlackBotDelete(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err := s.db.DeleteSlackBot(r.Context(), u.ID, id); err != nil {
		s.serverError(w, r, err)
		return
	}
	_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "channel", "slack_bot_delete", strconv.FormatInt(id, 10), "ok")
	http.Redirect(w, r, "/agents?msg=slack_deleted", http.StatusSeeOther)
}

// handleSlackUnpair removes one paired chat from an app.
func (s *Server) handleSlackUnpair(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	botID, _ := strconv.ParseInt(r.FormValue("bot_id"), 10, 64)
	externalID := strings.TrimSpace(r.FormValue("external_id"))
	if externalID == "" {
		http.Redirect(w, r, "/agents", http.StatusSeeOther)
		return
	}
	if err := s.db.DeleteIdentity(r.Context(), u.ID, "slack", botID, externalID); err != nil {
		s.serverError(w, r, err)
		return
	}
	_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "channel", "slack_unpair",
		"app "+strconv.FormatInt(botID, 10)+" chat "+externalID, "ok")
	http.Redirect(w, r, "/agents?msg=unpaired", http.StatusSeeOther)
}
