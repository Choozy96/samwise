package discord

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"samwise/internal/orchestrator"
	"samwise/internal/store"
)

const (
	channel          = "discord"
	pairingTTL       = 15 * time.Minute
	maxDownloadBytes = 45 << 20
)

// Bot handles one Discord bot's inbound messages (the Manager owns the Gateway
// session and routes MessageCreate events here). botID is its discord_bots row
// id; agentID, when non-zero, binds every message to that agent.
type Bot struct {
	session *discordgo.Session
	db      *store.DB
	orch    *orchestrator.Orchestrator
	log     *slog.Logger
	botID   int64
	agentID int64
	selfID  string // the bot's Discord user id, for mention detection
	http    *http.Client
}

// NewBot constructs the event handler for one bot.
func NewBot(session *discordgo.Session, db *store.DB, orch *orchestrator.Orchestrator, log *slog.Logger, botID, agentID int64, selfID string) *Bot {
	return &Bot{
		session: session, db: db, orch: orch, log: log,
		botID: botID, agentID: agentID, selfID: selfID,
		http: &http.Client{Timeout: 60 * time.Second},
	}
}

// mentionsBot reports whether content mentions the bot ("<@id>" or the
// nickname form "<@!id>").
func mentionsBot(content, selfID string) bool {
	if selfID == "" {
		return false
	}
	return strings.Contains(content, "<@"+selfID+">") || strings.Contains(content, "<@!"+selfID+">")
}

// stripMention removes the bot's own mention token(s).
func stripMention(content, selfID string) string {
	if selfID == "" {
		return strings.TrimSpace(content)
	}
	content = strings.ReplaceAll(content, "<@!"+selfID+">", "")
	content = strings.ReplaceAll(content, "<@"+selfID+">", "")
	return strings.TrimSpace(content)
}

// HandleMessageCreate processes one inbound message. Discord fires this for
// every message the bot can see (DMs + guild channels it's in).
func (b *Bot) HandleMessageCreate(ctx context.Context, m *discordgo.MessageCreate) {
	if m.Author == nil || m.Author.Bot || m.Author.ID == b.selfID {
		return // never converse with bots (incl. ourselves)
	}
	isDM := m.GuildID == "" // guild id empty = direct message
	text := strings.TrimSpace(m.Content)

	// A reply carries a reference to the message it points at — resolve it so
	// the agent can see THAT message's media and text ("@bot summarize this" on
	// an attachment post). The gateway usually inlines it; fetch as fallback.
	parent := m.ReferencedMessage
	if parent == nil && m.MessageReference != nil && m.MessageReference.MessageID != "" {
		parent, _ = b.session.ChannelMessage(m.ChannelID, m.MessageReference.MessageID)
	}

	// Guild channels: respond when mentioned OR when replying to the bot's own
	// message (Telegram-group semantics); un-mentioned messages otherwise only
	// in reply-to-everything mode.
	if !isDM {
		replyToBot := parent != nil && parent.Author != nil && parent.Author.ID == b.selfID
		if !mentionsBot(text, b.selfID) && !replyToBot && b.groupReplyMode(ctx, m.ChannelID) != "all" {
			return
		}
		text = stripMention(text, b.selfID)
	}
	attachments := m.Attachments
	if parent != nil {
		if len(parent.Attachments) > 0 {
			b.log.Info("discord: attaching media from replied-to message", "files", len(parent.Attachments))
			attachments = append(append([]*discordgo.MessageAttachment{}, attachments...), parent.Attachments...)
		}
		// Quote the replied-to text so the reply isn't contextless.
		if pt := strings.TrimSpace(parent.Content); pt != "" && text != "" {
			text = "[In reply to an earlier message: " + snippet(pt, 1500) + "]\n\n" + text
		}
	}
	if text == "" && len(attachments) == 0 {
		return
	}

	// Identity: a DM pairs the SENDER; a guild channel pairs the CHANNEL
	// (shared context). The DM conversation id is the chat we reply into.
	externalID := m.Author.ID
	if !isDM {
		externalID = m.ChannelID
	}
	ident, err := b.db.GetIdentityByExternal(ctx, channel, b.botID, externalID)
	if errors.Is(err, store.ErrNotFound) {
		b.handleUnpaired(ctx, externalID, m.ChannelID, !isDM)
		return
	}
	if err != nil {
		b.log.Error("discord identity lookup", "err", err)
		return
	}
	user, err := b.db.GetUserByID(ctx, ident.UserID)
	if err != nil || user.Disabled {
		return
	}

	// Bound agent (user-scoped; falls through to active agent).
	var boundAgent *store.Agent
	if b.agentID != 0 {
		if a, aerr := b.db.GetAgent(ctx, user.ID, b.agentID); aerr == nil {
			boundAgent = a
		}
	}

	// In a guild channel, only members whose own Discord account is paired may
	// perform writes (same rule as Telegram groups / Slack channels).
	readOnly := false
	if !isDM {
		if paired, perr := b.db.UserPairedOnChannel(ctx, channel, m.Author.ID); perr == nil && !paired {
			readOnly = true
		}
	}

	// Slash commands (as chat text — not Discord's native slash commands).
	if strings.HasPrefix(text, "/") {
		if readOnly {
			b.send(ctx, m.ChannelID, "🔒 Only registered users can run commands in a server channel. Pair your own Discord account with the assistant first (DM the bot).")
			return
		}
		if reply, handled := b.orch.TryCommand(ctx, user.ID, text); handled {
			b.send(ctx, m.ChannelID, reply)
			return
		}
	}

	// Attachments: Discord CDN URLs need no auth — plain capped download.
	var atts []orchestrator.Attachment
	for _, a := range attachments {
		if a == nil || a.URL == "" {
			continue
		}
		data, derr := b.download(ctx, a.URL)
		if derr != nil {
			b.log.Warn("discord attachment download", "name", a.Filename, "err", derr)
			continue
		}
		att, aerr := b.orch.SaveAttachment(user.ID, a.Filename, data)
		if aerr != nil {
			b.log.Warn("discord attachment save", "name", a.Filename, "err", aerr)
			continue
		}
		atts = append(atts, att)
	}

	// Typing indicator while the agent works (best-effort; Discord shows ~10s).
	_ = b.session.ChannelTyping(m.ChannelID)

	res, err := b.orch.Dispatch(ctx, orchestrator.DispatchRequest{
		User:             user,
		Channel:          channel,
		Agent:            boundAgent,
		UserMessage:      text,
		Attachments:      atts,
		StoreUserMessage: true,
		ReadOnly:         readOnly,
		Origin:           orchestrator.Address{Channel: channel, BotID: b.botID, ChatID: m.ChannelID},
	}, nil)
	if err != nil {
		b.log.Error("discord dispatch", "user_id", user.ID, "err", err)
		b.send(ctx, m.ChannelID, "Something went wrong handling that — try again in a moment.")
		return
	}
	if res != nil && strings.TrimSpace(res.FinalText) != "" {
		b.send(ctx, m.ChannelID, res.FinalText)
	}
}

// groupReplyMode reads the paired owner's group-reply preference for a guild
// channel ("mention" default; unpaired channels default to mention-only).
func (b *Bot) groupReplyMode(ctx context.Context, channelID string) string {
	ident, err := b.db.GetIdentityByExternal(ctx, channel, b.botID, channelID)
	if err != nil {
		return "mention"
	}
	if st, serr := b.db.GetSettings(ctx, ident.UserID); serr == nil && st.GroupReplyMode != "" {
		return st.GroupReplyMode
	}
	return "mention"
}

// handleUnpaired issues a pairing code (DM: for the sender; channel: for the
// channel) — same flow as Telegram/Slack.
func (b *Bot) handleUnpaired(ctx context.Context, externalID, chatID string, group bool) {
	code, err := newPairingCode()
	if err != nil {
		b.log.Error("discord pairing code gen", "err", err)
		return
	}
	expires := time.Now().Add(pairingTTL).UTC().Format("2006-01-02 15:04:05")
	if err := b.db.UpsertPairingCode(ctx, code, channel, b.botID, externalID, chatID, expires); err != nil {
		b.log.Error("discord pairing code store", "err", err)
		return
	}
	b.log.Info("discord pairing code issued", "external_id", externalID, "group", group)
	what := "this chat"
	if group {
		what = "this channel"
	}
	b.send(ctx, chatID, fmt.Sprintf(
		"👋 To connect %s to your assistant, log into the web portal, open the Agents page, and enter this code within 15 minutes:\n\n%s",
		what, code))
}

// send delivers text (Discord renders the agent's markdown natively — only
// chunking is needed), best-effort with logging.
func (b *Bot) send(_ context.Context, chatID, text string) {
	for _, part := range chunk(text, discordMaxLen) {
		if _, err := b.session.ChannelMessageSend(chatID, part); err != nil {
			b.log.Warn("discord send failed", "chat", chatID, "err", err)
			return // don't spray remaining chunks after a failure
		}
	}
}

// download fetches a CDN attachment with a size cap.
func (b *Bot) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discord: attachment fetch status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDownloadBytes {
		return nil, fmt.Errorf("discord: attachment exceeds %d bytes", maxDownloadBytes)
	}
	return data, nil
}

// newPairingCode returns a 6-character unambiguous uppercase code.
func newPairingCode() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no I,O,0,1
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i, c := range buf {
		buf[i] = alphabet[int(c)%len(alphabet)]
	}
	return string(buf), nil
}
