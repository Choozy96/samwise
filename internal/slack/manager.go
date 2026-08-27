package slack

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	slackapi "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"samwise/internal/orchestrator"
	"samwise/internal/secretbox"
	"samwise/internal/store"
)

// reconcileEvery is how often the Manager re-syncs its running Socket Mode
// connections with the slack_bots table (same cadence as the Telegram Manager).
const reconcileEvery = 30 * time.Second

// Manager runs one Socket Mode connection per enabled Slack app and is the
// orchestrator's "slack" ChannelSender. The Slack mirror of telegram.Manager.
type Manager struct {
	db   *store.DB
	orch *orchestrator.Orchestrator
	box  *secretbox.Box
	log  *slog.Logger

	mu      sync.Mutex
	running map[int64]*appHandle
}

type appHandle struct {
	client      *Client
	cancel      context.CancelFunc
	fingerprint string
}

// NewManager constructs the Manager.
func NewManager(db *store.DB, orch *orchestrator.Orchestrator, box *secretbox.Box, log *slog.Logger) *Manager {
	return &Manager{db: db, orch: orch, box: box, log: log, running: map[int64]*appHandle{}}
}

// Run reconciles connections until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) {
	m.reconcile(ctx)
	t := time.NewTicker(reconcileEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			m.stopAll()
			return
		case <-t.C:
			m.reconcile(ctx)
		}
	}
}

func (m *Manager) reconcile(ctx context.Context) {
	if !m.box.Enabled() {
		return // no MASTER_KEY: tokens can't be decrypted, nothing to run
	}
	bots, err := m.db.ListEnabledSlackBots(ctx)
	if err != nil {
		m.log.Error("slack manager: list bots", "err", err)
		return
	}
	desired := map[int64]store.SlackBot{}
	for _, b := range bots {
		desired[b.ID] = b
	}

	// Stop connections that are gone or whose tokens/agent changed.
	m.mu.Lock()
	for id, h := range m.running {
		b, ok := desired[id]
		if !ok || fingerprint(b) != h.fingerprint {
			h.cancel()
			delete(m.running, id)
		}
	}
	var toStart []store.SlackBot
	for id, b := range desired {
		if _, ok := m.running[id]; !ok {
			toStart = append(toStart, b)
		}
	}
	m.mu.Unlock()

	for _, b := range toStart {
		m.start(ctx, b)
	}
}

func fingerprint(b store.SlackBot) string {
	return b.BotTokenEnc + "|" + b.AppTokenEnc + "|" + strconv.FormatInt(b.AgentID, 10)
}

// start decrypts an app's tokens, validates them (auth.test → cached identity),
// and launches its Socket Mode loop.
func (m *Manager) start(ctx context.Context, sb store.SlackBot) {
	botTok, err1 := m.box.Decrypt(sb.BotTokenEnc)
	appTok, err2 := m.box.Decrypt(sb.AppTokenEnc)
	if err1 != nil || err2 != nil {
		m.log.Error("slack manager: decrypt tokens", "bot_id", sb.ID)
		return
	}
	api := slackapi.New(string(botTok), slackapi.OptionAppLevelToken(string(appTok)))
	client := &Client{api: api}

	// Validate + refresh the cached identity (best-effort on the name; the bot
	// user id is required for mention handling).
	selfID := sb.BotUserID
	mc, cancelAuth := context.WithTimeout(ctx, 10*time.Second)
	if uid, team, err := client.AuthTest(mc); err == nil {
		selfID = uid
		if uid != sb.BotUserID || team != sb.TeamName {
			_ = m.db.SetSlackBotIdentity(ctx, sb.ID, uid, team)
		}
	} else {
		m.log.Warn("slack manager: auth.test failed (token may be invalid)", "bot_id", sb.ID, "err", err)
	}
	cancelAuth()

	bctx, cancel := context.WithCancel(ctx)
	bot := NewBot(client, m.db, m.orch, m.log, sb.ID, sb.AgentID, selfID)
	sm := socketmode.New(api)

	m.mu.Lock()
	m.running[sb.ID] = &appHandle{client: client, cancel: cancel, fingerprint: fingerprint(sb)}
	m.mu.Unlock()

	go m.runSocket(bctx, sm, bot, sb.ID)
	m.log.Info("slack manager: socket loop started", "bot_id", sb.ID, "agent_id", sb.AgentID)
}

// runSocket owns one app's Socket Mode connection: acks every request and
// routes Events API events to the Bot. socketmode reconnects internally; if the
// loop exits (bad token), the next reconcile restarts it.
func (m *Manager) runSocket(ctx context.Context, sm *socketmode.Client, bot *Bot, botID int64) {
	go func() {
		if err := sm.RunContext(ctx); err != nil && ctx.Err() == nil {
			m.log.Warn("slack socket loop ended", "bot_id", botID, "err", err)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-sm.Events:
			if !ok {
				return
			}
			// Ack first: Slack retries un-acked envelopes, which would duplicate
			// agent runs.
			if evt.Request != nil {
				sm.Ack(*evt.Request)
			}
			switch evt.Type {
			case socketmode.EventTypeEventsAPI:
				e, ok := evt.Data.(slackevents.EventsAPIEvent)
				if !ok {
					continue
				}
				var payload []byte
				if evt.Request != nil {
					payload = evt.Request.Payload
				}
				bot.HandleEvent(ctx, e, payload)
			case socketmode.EventTypeConnectionError:
				m.log.Warn("slack socket connection error", "bot_id", botID)
			}
		}
	}
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, h := range m.running {
		h.cancel()
		delete(m.running, id)
	}
}

func (m *Manager) clientFor(botID int64) *Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	if h, ok := m.running[botID]; ok {
		return h.client
	}
	return nil
}

func errNoApp(userID int64) error {
	return fmt.Errorf("slack: no running app/paired chat for user %d", userID)
}

// ── ChannelSender ────────────────────────────────────────────────────────────

// Send delivers via the user's primary app (lowest bot id they're paired to).
func (m *Manager) Send(ctx context.Context, userID int64, text string) error {
	botID, ident, ok := m.primaryApp(ctx, userID)
	if !ok {
		return errNoApp(userID)
	}
	return m.SendToChat(ctx, userID, botID, ident.ChatID, text)
}

// SendAgent delivers via the app bound to agentID (if running and paired),
// else the primary app.
func (m *Manager) SendAgent(ctx context.Context, userID, agentID int64, text string) error {
	if b, err := m.db.SlackBotAgentBinding(ctx, userID, agentID); err == nil && b != nil {
		if m.clientFor(b.ID) != nil {
			if ident, ierr := m.db.GetIdentityByUserBot(ctx, userID, channel, b.ID); ierr == nil {
				return m.SendToChat(ctx, userID, b.ID, ident.ChatID, text)
			}
		}
	}
	return m.Send(ctx, userID, text)
}

// SendBot delivers via a specific app id, to the user's paired chat on it.
func (m *Manager) SendBot(ctx context.Context, userID, botID int64, text string) error {
	ident, err := m.db.GetIdentityByUserBot(ctx, userID, channel, botID)
	if err != nil {
		return fmt.Errorf("slack: no paired chat for user %d on app %d: %w", userID, botID, err)
	}
	return m.SendToChat(ctx, userID, botID, ident.ChatID, text)
}

// SendToChat delivers to an explicit app+conversation.
func (m *Manager) SendToChat(ctx context.Context, userID, botID int64, chatID, text string) error {
	client := m.clientFor(botID)
	if client == nil {
		return errNoApp(userID)
	}
	return deliver(ctx, client, chatID, "", text, m.log)
}

// SendFile uploads a document to the user's primary-app chat.
func (m *Manager) SendFile(ctx context.Context, userID int64, name string, data []byte, caption string) error {
	botID, ident, ok := m.primaryApp(ctx, userID)
	if !ok {
		return errNoApp(userID)
	}
	return m.SendFileToChat(ctx, userID, botID, ident.ChatID, name, data, caption)
}

// SendFileToChat uploads a document to an explicit app+conversation.
func (m *Manager) SendFileToChat(ctx context.Context, userID, botID int64, chatID, name string, data []byte, caption string) error {
	client := m.clientFor(botID)
	if client == nil {
		return errNoApp(userID)
	}
	return client.UploadFile(ctx, chatID, "", name, data, caption)
}

// primaryApp picks the user's primary Slack app: the first RUNNING app they're
// paired to, by bot id.
func (m *Manager) primaryApp(ctx context.Context, userID int64) (int64, *store.ChannelIdentity, bool) {
	idents, err := m.db.ListIdentitiesByUser(ctx, userID, channel)
	if err != nil {
		return 0, nil, false
	}
	for i := range idents {
		id := idents[i]
		if id.ChatID == "" {
			continue
		}
		if m.clientFor(id.BotID) != nil {
			return id.BotID, &id, true
		}
	}
	return 0, nil, false
}
