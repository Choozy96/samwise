package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"samwise/internal/orchestrator"
	"samwise/internal/secretbox"
	"samwise/internal/store"
)

// reconcileEvery is how often the Manager re-syncs its running pollers with the
// telegram_bots table, so adding/editing/disabling a bot in the portal takes
// effect within this window without a restart.
const reconcileEvery = 30 * time.Second

// Manager runs one inbound poller per Telegram bot and is the orchestrator's
// outbound ChannelSender, routing each delivery to the correct bot. Bots come
// from two sources: the optional legacy .env token (botID 0, unbound) and the
// per-user telegram_bots table (botID > 0, each bound to an agent).
type Manager struct {
	db          *store.DB
	orch        *orchestrator.Orchestrator
	box         *secretbox.Box
	log         *slog.Logger
	legacyToken string

	mu      sync.RWMutex
	running map[int64]*botHandle // botID -> handle
}

// botHandle tracks one running poller. fingerprint changes (token/agent edit)
// trigger a restart on the next reconcile.
type botHandle struct {
	client      *Client
	cancel      context.CancelFunc
	fingerprint string
}

// NewManager constructs the Manager. legacyToken may be "" (no legacy bot).
func NewManager(db *store.DB, orch *orchestrator.Orchestrator, box *secretbox.Box, log *slog.Logger, legacyToken string) *Manager {
	return &Manager{
		db: db, orch: orch, box: box, log: log, legacyToken: legacyToken,
		running: map[int64]*botHandle{},
	}
}

// Run reconciles immediately, then on a ticker, until ctx is cancelled. On exit
// it stops every poller.
func (m *Manager) Run(ctx context.Context) {
	m.log.Info("telegram manager started")
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

// desiredBot is one bot the Manager should be running.
type desiredBot struct {
	token       string
	agentID     int64
	fingerprint string
	bot         *store.TelegramBot // nil for the legacy bot
}

// reconcile diffs the desired bot set against running pollers, starting and
// stopping as needed. It runs serially (only from Run), so it's the sole writer
// of m.running.
func (m *Manager) reconcile(ctx context.Context) {
	desired := map[int64]desiredBot{}
	if m.legacyToken != "" {
		desired[0] = desiredBot{token: m.legacyToken, agentID: 0, fingerprint: "legacy"}
	}
	if m.box.Enabled() {
		bots, err := m.db.ListEnabledTelegramBots(ctx)
		if err != nil {
			m.log.Error("telegram manager: list bots", "err", err)
		}
		for i := range bots {
			b := bots[i]
			tok, derr := m.box.Decrypt(b.TokenEnc)
			if derr != nil {
				m.log.Error("telegram manager: decrypt bot token", "bot_id", b.ID, "err", derr)
				continue
			}
			desired[b.ID] = desiredBot{
				token:       string(tok),
				agentID:     b.AgentID,
				fingerprint: b.TokenEnc + "|" + strconv.FormatInt(b.AgentID, 10),
				bot:         &b,
			}
		}
	}

	// Stop pollers that are gone or whose token/agent changed.
	m.mu.Lock()
	for id, h := range m.running {
		if d, ok := desired[id]; !ok || d.fingerprint != h.fingerprint {
			h.cancel()
			delete(m.running, id)
		}
	}
	// Determine which to start (still under lock to avoid double-starts).
	var toStart []desiredBot
	var startIDs []int64
	for id, d := range desired {
		if _, ok := m.running[id]; !ok {
			toStart = append(toStart, d)
			startIDs = append(startIDs, id)
		}
	}
	m.mu.Unlock()

	for i, d := range toStart {
		m.start(ctx, startIDs[i], d)
	}
}

// start launches a poller for one bot and records its handle. For DB bots it
// caches the bot's @username via getMe (best-effort) so the portal can show it.
func (m *Manager) start(ctx context.Context, botID int64, d desiredBot) {
	client := NewClient(d.token)
	if d.bot != nil {
		mc, cancel := context.WithTimeout(ctx, 10*time.Second)
		if uname, err := client.GetMe(mc); err != nil {
			m.log.Warn("telegram manager: getMe failed (token may be invalid)", "bot_id", botID, "err", err)
		} else if uname != d.bot.Username {
			_ = m.db.SetTelegramBotUsername(ctx, botID, uname)
		}
		cancel()
	}

	bctx, cancel := context.WithCancel(ctx)
	bot := NewBot(client, m.db, m.orch, m.log, botID, d.agentID)
	m.mu.Lock()
	m.running[botID] = &botHandle{client: client, cancel: cancel, fingerprint: d.fingerprint}
	m.mu.Unlock()
	go bot.Run(bctx)
	m.log.Info("telegram manager: bot poller started", "bot_id", botID, "agent_id", d.agentID)
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, h := range m.running {
		h.cancel()
		delete(m.running, id)
	}
}

// clientFor returns the running client for a bot id, if any.
func (m *Manager) clientFor(botID int64) *Client {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if h, ok := m.running[botID]; ok {
		return h.client
	}
	return nil
}

// ── orchestrator.ChannelSender ──────────────────────────────────────────────

// Send delivers to the user's primary bot (the first running bot they're paired
// to, legacy bot preferred for backward compatibility).
func (m *Manager) Send(ctx context.Context, userID int64, text string) error {
	botID, ok := m.primaryBot(ctx, userID)
	if !ok {
		return errNoBot(userID)
	}
	return m.SendBot(ctx, userID, botID, text)
}

// SendAgent delivers via the bot bound to agentID (if the user has one and it's
// running), else their primary bot.
func (m *Manager) SendAgent(ctx context.Context, userID, agentID int64, text string) error {
	if b, err := m.db.BotAgentBinding(ctx, userID, agentID); err == nil && b != nil {
		if m.clientFor(b.ID) != nil {
			if _, ierr := m.db.GetIdentityByUserBot(ctx, userID, channel, b.ID); ierr == nil {
				return m.SendBot(ctx, userID, b.ID, text)
			}
		}
	}
	return m.Send(ctx, userID, text)
}

// SendBot delivers via a specific bot id, to the user's paired chat on that bot.
func (m *Manager) SendBot(ctx context.Context, userID, botID int64, text string) error {
	client := m.clientFor(botID)
	if client == nil {
		return errNoBot(userID)
	}
	ident, err := m.db.GetIdentityByUserBot(ctx, userID, channel, botID)
	if err != nil {
		return fmt.Errorf("telegram: no paired chat for user %d on bot %d: %w", userID, botID, err)
	}
	chatID, err := strconv.ParseInt(ident.ChatID, 10, 64)
	if err != nil {
		return fmt.Errorf("telegram: bad chat id %q: %w", ident.ChatID, err)
	}
	format := FormatMarkdown
	if st, serr := m.db.GetSettings(ctx, userID); serr == nil && st.TgFormat != "" {
		format = st.TgFormat
	}
	return deliver(ctx, client, chatID, text, format, m.log)
}

// primaryBot picks the user's primary bot: the first running bot they're paired
// to, iterating identities by bot_id (so the legacy bot, id 0, is preferred).
func (m *Manager) primaryBot(ctx context.Context, userID int64) (int64, bool) {
	idents, err := m.db.ListIdentitiesByUser(ctx, userID, channel)
	if err != nil {
		return 0, false
	}
	for _, id := range idents {
		if m.clientFor(id.BotID) != nil {
			return id.BotID, true
		}
	}
	return 0, false
}

func errNoBot(userID int64) error {
	return fmt.Errorf("telegram: no running bot paired for user %d", userID)
}
