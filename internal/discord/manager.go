package discord

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"samwise/internal/orchestrator"
	"samwise/internal/secretbox"
	"samwise/internal/store"
)

// reconcileEvery matches the Telegram/Slack Managers' cadence.
const reconcileEvery = 30 * time.Second

// Manager runs one Gateway session per enabled Discord bot and is the
// orchestrator's "discord" ChannelSender.
type Manager struct {
	db   *store.DB
	orch *orchestrator.Orchestrator
	box  *secretbox.Box
	log  *slog.Logger

	mu      sync.Mutex
	running map[int64]*botHandle
}

type botHandle struct {
	session     *discordgo.Session
	cancel      context.CancelFunc
	fingerprint string
}

// NewManager constructs the Manager.
func NewManager(db *store.DB, orch *orchestrator.Orchestrator, box *secretbox.Box, log *slog.Logger) *Manager {
	return &Manager{db: db, orch: orch, box: box, log: log, running: map[int64]*botHandle{}}
}

// Run reconciles sessions until ctx is cancelled.
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
		return
	}
	bots, err := m.db.ListEnabledDiscordBots(ctx)
	if err != nil {
		m.log.Error("discord manager: list bots", "err", err)
		return
	}
	desired := map[int64]store.DiscordBot{}
	for _, b := range bots {
		desired[b.ID] = b
	}

	m.mu.Lock()
	for id, h := range m.running {
		b, ok := desired[id]
		if !ok || fingerprint(b) != h.fingerprint {
			h.cancel()
			delete(m.running, id)
		}
	}
	var toStart []store.DiscordBot
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

func fingerprint(b store.DiscordBot) string {
	return b.TokenEnc + "|" + strconv.FormatInt(b.AgentID, 10)
}

// start decrypts a bot's token, opens its Gateway session, and wires the
// message handler.
func (m *Manager) start(ctx context.Context, db store.DiscordBot) {
	tok, err := m.box.Decrypt(db.TokenEnc)
	if err != nil {
		m.log.Error("discord manager: decrypt token", "bot_id", db.ID)
		return
	}
	session, err := discordgo.New("Bot " + strings.TrimSpace(string(tok)))
	if err != nil {
		m.log.Error("discord manager: session", "bot_id", db.ID, "err", err)
		return
	}
	// DMs + guild messages, with message content (a privileged intent — must be
	// enabled on the app's Bot page in the developer portal).
	session.Identify.Intents = discordgo.IntentsGuildMessages |
		discordgo.IntentsDirectMessages | discordgo.IntentMessageContent

	bctx, cancel := context.WithCancel(ctx)
	bot := NewBot(session, m.db, m.orch, m.log, db.ID, db.AgentID, db.BotUserID)
	session.AddHandler(func(_ *discordgo.Session, mc *discordgo.MessageCreate) {
		if bctx.Err() != nil {
			return
		}
		bot.HandleMessageCreate(bctx, mc)
	})

	if err := session.Open(); err != nil {
		cancel()
		m.log.Warn("discord manager: gateway open failed (token may be invalid)", "bot_id", db.ID, "err", err)
		return
	}
	// Cache/refresh the bot's identity from the ready state (needed for mention
	// detection; also fixes selfID when a token was replaced).
	if session.State != nil && session.State.User != nil {
		u := session.State.User
		bot.selfID = u.ID
		if u.ID != db.BotUserID || u.Username != db.Username {
			_ = m.db.SetDiscordBotIdentity(ctx, db.ID, u.ID, u.Username)
		}
	}

	m.mu.Lock()
	m.running[db.ID] = &botHandle{session: session, cancel: cancel, fingerprint: fingerprint(db)}
	m.mu.Unlock()

	go func() {
		<-bctx.Done()
		_ = session.Close()
	}()
	m.log.Info("discord manager: gateway session started", "bot_id", db.ID, "agent_id", db.AgentID)
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, h := range m.running {
		h.cancel()
		delete(m.running, id)
	}
}

func (m *Manager) sessionFor(botID int64) *discordgo.Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if h, ok := m.running[botID]; ok {
		return h.session
	}
	return nil
}

func errNoBot(userID int64) error {
	return fmt.Errorf("discord: no running bot/paired chat for user %d", userID)
}

// ── ChannelSender ────────────────────────────────────────────────────────────

// Send delivers via the user's primary bot (first running bot they're paired to).
func (m *Manager) Send(ctx context.Context, userID int64, text string) error {
	botID, ident, ok := m.primaryBot(ctx, userID)
	if !ok {
		return errNoBot(userID)
	}
	return m.SendToChat(ctx, userID, botID, ident.ChatID, text)
}

// SendAgent delivers via the bot bound to agentID (if running and paired),
// else the primary bot.
func (m *Manager) SendAgent(ctx context.Context, userID, agentID int64, text string) error {
	if b, err := m.db.DiscordBotAgentBinding(ctx, userID, agentID); err == nil && b != nil {
		if m.sessionFor(b.ID) != nil {
			if ident, ierr := m.db.GetIdentityByUserBot(ctx, userID, channel, b.ID); ierr == nil {
				return m.SendToChat(ctx, userID, b.ID, ident.ChatID, text)
			}
		}
	}
	return m.Send(ctx, userID, text)
}

// SendBot delivers via a specific bot id, to the user's paired chat on it.
func (m *Manager) SendBot(ctx context.Context, userID, botID int64, text string) error {
	ident, err := m.db.GetIdentityByUserBot(ctx, userID, channel, botID)
	if err != nil {
		return fmt.Errorf("discord: no paired chat for user %d on bot %d: %w", userID, botID, err)
	}
	return m.SendToChat(ctx, userID, botID, ident.ChatID, text)
}

// SendToChat delivers to an explicit bot+channel.
func (m *Manager) SendToChat(_ context.Context, userID, botID int64, chatID, text string) error {
	session := m.sessionFor(botID)
	if session == nil {
		return errNoBot(userID)
	}
	for _, part := range chunk(text, discordMaxLen) {
		if _, err := session.ChannelMessageSend(chatID, part); err != nil {
			return err
		}
	}
	return nil
}

// SendFile uploads a document to the user's primary-bot chat.
func (m *Manager) SendFile(ctx context.Context, userID int64, name string, data []byte, caption string) error {
	botID, ident, ok := m.primaryBot(ctx, userID)
	if !ok {
		return errNoBot(userID)
	}
	return m.SendFileToChat(ctx, userID, botID, ident.ChatID, name, data, caption)
}

// SendFileToChat uploads a document to an explicit bot+channel.
func (m *Manager) SendFileToChat(_ context.Context, userID, botID int64, chatID, name string, data []byte, caption string) error {
	session := m.sessionFor(botID)
	if session == nil {
		return errNoBot(userID)
	}
	_, err := session.ChannelMessageSendComplex(chatID, &discordgo.MessageSend{
		Content: caption,
		Files:   []*discordgo.File{{Name: name, Reader: bytes.NewReader(data)}},
	})
	return err
}

// primaryBot picks the user's primary Discord bot: the first RUNNING bot
// they're paired to, by bot id.
func (m *Manager) primaryBot(ctx context.Context, userID int64) (int64, *store.ChannelIdentity, bool) {
	idents, err := m.db.ListIdentitiesByUser(ctx, userID, channel)
	if err != nil {
		return 0, nil, false
	}
	for i := range idents {
		id := idents[i]
		if id.ChatID == "" {
			continue
		}
		if m.sessionFor(id.BotID) != nil {
			return id.BotID, &id, true
		}
	}
	return 0, nil, false
}

// ValidateToken checks a bot token over plain REST (no Gateway) and returns
// the bot's user id + username — used by the web layer when a bot is added.
func ValidateToken(ctx context.Context, token string) (botUserID, username string, err error) {
	s, err := discordgo.New("Bot " + strings.TrimSpace(token))
	if err != nil {
		return "", "", err
	}
	u, err := s.User("@me", discordgo.WithContext(ctx))
	if err != nil {
		return "", "", err
	}
	return u.ID, u.Username, nil
}
