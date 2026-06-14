package telegram

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"samwise/internal/orchestrator"
	"samwise/internal/store"
)

const (
	channel       = "telegram"
	pairingTTL    = 15 * time.Minute
	tgMaxLen      = 4096
	pollTimeout   = 30 // seconds (long poll)
	pollBackoffMs = 3000
)

// Bot is the inbound long-polling loop plus pairing handling for a single
// Telegram bot. botID is its telegram_bots row id (0 = the legacy .env-token
// bot); agentID, when non-zero, binds every message on this bot to that agent's
// persona/thread instead of the user's active agent.
type Bot struct {
	client  *Client
	db      *store.DB
	orch    *orchestrator.Orchestrator
	log     *slog.Logger
	botID   int64
	agentID int64
}

// NewBot constructs the inbound bot. botID 0 and agentID 0 reproduce the legacy
// single-bot, active-agent behavior.
func NewBot(client *Client, db *store.DB, orch *orchestrator.Orchestrator, log *slog.Logger, botID, agentID int64) *Bot {
	return &Bot{client: client, db: db, orch: orch, log: log, botID: botID, agentID: agentID}
}

// Run polls for updates until ctx is cancelled. Inbound long-polling reconnects
// automatically on error (spec §11).
func (b *Bot) Run(ctx context.Context) {
	b.log.Info("telegram bot started")
	var offset int64
	for {
		if ctx.Err() != nil {
			return
		}
		updates, err := b.client.GetUpdates(ctx, offset, pollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			b.log.Warn("telegram getUpdates failed, backing off", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollBackoffMs * time.Millisecond):
			}
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			b.handle(ctx, u)
		}
	}
}

func (b *Bot) handle(ctx context.Context, u Update) {
	msg := u.Message
	if msg == nil || msg.From == nil || msg.Chat == nil {
		return
	}
	// A captioned file carries its text in Caption, not Text.
	text := msg.Text
	if text == "" {
		text = msg.Caption
	}
	hasFile := msg.Document != nil || len(msg.Photo) > 0 || msg.Video != nil ||
		msg.Animation != nil || msg.Voice != nil || msg.Audio != nil ||
		msg.VideoNote != nil || msg.Sticker != nil
	if text == "" && !hasFile {
		// Nothing actionable (service message, etc.).
		return
	}
	// A sticker stands in for an emoji — give the agent that context even if it
	// can't view an animated/video sticker.
	if text == "" && msg.Sticker != nil && msg.Sticker.Emoji != "" {
		text = "[sticker: " + msg.Sticker.Emoji + "]"
	}
	externalID := strconv.FormatInt(msg.From.ID, 10)

	ident, err := b.db.GetIdentityByExternal(ctx, channel, b.botID, externalID)
	if errors.Is(err, store.ErrNotFound) {
		b.handleUnpaired(ctx, externalID, msg.Chat.ID)
		return
	}
	if err != nil {
		b.log.Error("telegram identity lookup", "err", err)
		return
	}

	user, err := b.db.GetUserByID(ctx, ident.UserID)
	if err != nil || user.Disabled {
		// Paired to a missing/disabled account: drop silently.
		return
	}

	// Resolve this bot's bound agent (if any) for the paired user. GetAgent is
	// user-scoped, so a binding that isn't this user's agent simply falls through
	// to their active agent — no cross-user leakage.
	var boundAgent *store.Agent
	if b.agentID != 0 {
		if a, aerr := b.db.GetAgent(ctx, user.ID, b.agentID); aerr == nil {
			boundAgent = a
		}
	}

	// Slash commands (/model, /runtime, /status, /help) work over Telegram too.
	if text != "" {
		// A bound bot always speaks as its agent, so /agent switching here would be
		// misleading — explain instead of silently changing the (unused) active agent.
		if boundAgent != nil && isAgentSwitch(text) {
			_ = b.send(ctx, msg.Chat.ID, "This bot is bound to the “"+boundAgent.Name+"” agent, so /agent doesn’t apply here. "+
				"Use the web portal to switch the agent a bot is bound to.")
			return
		}
		if reply, handled := b.orch.TryCommand(ctx, user.ID, text); handled {
			_ = b.send(ctx, msg.Chat.ID, reply)
			return
		}
	}

	// Show a "typing…" indicator while the agent works (refreshed on a loop,
	// since each action lasts only ~5s).
	typingCtx, stopTyping := context.WithCancel(ctx)
	go b.keepTyping(typingCtx, msg.Chat.ID)

	// Download any attached file(s) into the user's workspace so the agent's
	// tools can read them.
	atts := b.downloadAttachments(ctx, user.ID, msg)

	// Dispatch to this bot's bound agent (boundAgent), or the user's active agent
	// when the bot is unbound (boundAgent == nil).
	res, err := b.orch.Dispatch(ctx, orchestrator.DispatchRequest{
		User:             user,
		Channel:          channel,
		Agent:            boundAgent,
		UserMessage:      text,
		Attachments:      atts,
		StoreUserMessage: true,
	}, nil)
	stopTyping()
	if err != nil {
		b.log.Error("telegram dispatch", "user_id", user.ID, "err", err)
		_ = b.send(ctx, msg.Chat.ID, "⚠️ Something went wrong handling that. Please try again.")
		return
	}
	if res.FinalText != "" {
		_ = b.sendAs(ctx, msg.Chat.ID, res.FinalText, b.userFormat(ctx, user.ID))
	}
}

// userFormat returns the user's Telegram message format (default markdown).
func (b *Bot) userFormat(ctx context.Context, userID int64) string {
	if s, err := b.db.GetSettings(ctx, userID); err == nil && s.TgFormat != "" {
		return s.TgFormat
	}
	return FormatMarkdown
}

// downloadAttachments fetches a message's document or photo from Telegram and
// saves it to the user's workspace as an orchestrator attachment. Best-effort:
// a file that's too large or fails to download is skipped (logged), not fatal.
func (b *Bot) downloadAttachments(ctx context.Context, userID int64, msg *Message) []orchestrator.Attachment {
	type ref struct {
		fileID string
		name   string
		size   int64
	}
	var refs []ref
	switch {
	case msg.Document != nil:
		refs = append(refs, ref{msg.Document.FileID, msg.Document.FileName, msg.Document.FileSize})
	case len(msg.Photo) > 0:
		// Telegram sends ascending sizes; the last is the highest resolution.
		p := msg.Photo[len(msg.Photo)-1]
		refs = append(refs, ref{p.FileID, "photo.jpg", p.FileSize})
	case msg.Video != nil:
		refs = append(refs, ref{msg.Video.FileID, mediaName(msg.Video.FileName, "video.mp4"), msg.Video.FileSize})
	case msg.Animation != nil:
		refs = append(refs, ref{msg.Animation.FileID, mediaName(msg.Animation.FileName, "animation.mp4"), msg.Animation.FileSize})
	case msg.VideoNote != nil:
		refs = append(refs, ref{msg.VideoNote.FileID, "video-note.mp4", msg.VideoNote.FileSize})
	case msg.Voice != nil:
		refs = append(refs, ref{msg.Voice.FileID, "voice.ogg", msg.Voice.FileSize})
	case msg.Audio != nil:
		refs = append(refs, ref{msg.Audio.FileID, mediaName(msg.Audio.FileName, "audio.mp3"), msg.Audio.FileSize})
	case msg.Sticker != nil:
		// Static stickers are .webp (the agent can view them as an image);
		// animated/video stickers aren't viewable but degrade gracefully.
		name := "sticker.webp"
		if msg.Sticker.IsVideo {
			name = "sticker.webm"
		} else if msg.Sticker.IsAnimated {
			name = "sticker.tgs"
		}
		refs = append(refs, ref{msg.Sticker.FileID, name, msg.Sticker.FileSize})
	}

	var out []orchestrator.Attachment
	for _, r := range refs {
		if r.size > orchestrator.MaxAttachmentBytes {
			b.log.Warn("telegram attachment too large, skipping", "user_id", userID, "size", r.size)
			_ = b.db.AddAuditEvent(ctx, userID, 0, "message", "attachment", r.name, "skipped (too large)")
			continue
		}
		path, err := b.client.GetFile(ctx, r.fileID)
		if err != nil {
			b.log.Error("telegram getFile", "err", err)
			continue
		}
		data, err := b.client.DownloadFile(ctx, path, orchestrator.MaxAttachmentBytes)
		if err != nil {
			b.log.Error("telegram file download", "err", err)
			continue
		}
		att, err := b.orch.SaveAttachment(userID, r.name, data)
		if err != nil {
			b.log.Error("telegram save attachment", "err", err)
			continue
		}
		out = append(out, att)
		_ = b.db.AddAuditEvent(ctx, userID, 0, "message", "attachment", att.Name, "ok")
	}
	return out
}

// mediaName returns name if Telegram supplied one, else a sensible default (some
// media objects — voice, video notes — have no file name).
func mediaName(name, def string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	return def
}

// keepTyping refreshes the "typing…" chat action until ctx is cancelled. Each
// action expires after ~5s, so it re-sends every 4s.
func (b *Bot) keepTyping(ctx context.Context, chatID int64) {
	for {
		if err := b.client.SendChatAction(ctx, chatID, "typing"); err != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(4 * time.Second):
		}
	}
}

// handleUnpaired issues a pairing code to an unknown sender (spec §4.1). This is
// the only reply an unpaired sender gets; it lets the owner link the account
// from the portal.
func (b *Bot) handleUnpaired(ctx context.Context, externalID string, chatID int64) {
	code, err := newPairingCode()
	if err != nil {
		b.log.Error("telegram pairing code gen", "err", err)
		return
	}
	expires := time.Now().Add(pairingTTL).UTC().Format("2006-01-02 15:04:05")
	if err := b.db.UpsertPairingCode(ctx, code, channel, b.botID, externalID, strconv.FormatInt(chatID, 10), expires); err != nil {
		b.log.Error("telegram pairing code store", "err", err)
		return
	}
	b.log.Info("telegram pairing code issued", "external_id", externalID)
	_ = b.send(ctx, chatID, fmt.Sprintf(
		"👋 To connect this chat to your assistant, log into the web portal, open the Agents page, and enter this code within 15 minutes:\n\n%s", code))
}

// send delivers text as plain Telegram text (used for pairing/error/command
// replies, which are simple). The agent's reply uses sendAs with the user's format.
func (b *Bot) send(ctx context.Context, chatID int64, text string) error {
	return b.sendAs(ctx, chatID, text, FormatMarkdown)
}

// sendAs delivers text applying the given format (markdown=raw, telegram=HTML),
// chunked with transient-retry and a plain-text fallback (see deliver).
func (b *Bot) sendAs(ctx context.Context, chatID int64, text, format string) error {
	if err := deliver(ctx, b.client, chatID, text, format, b.log); err != nil {
		b.log.Error("telegram send failed", "chat_id", chatID, "err", err)
		return err
	}
	return nil
}

// isAgentSwitch reports whether text is an /agent command attempting to switch
// agents (i.e. has an argument). Bare "/agent" (list) is left to TryCommand.
func isAgentSwitch(text string) bool {
	f := strings.Fields(strings.TrimSpace(text))
	return len(f) >= 2 && (f[0] == "/agent" || strings.HasPrefix(f[0], "/agent@"))
}

// newPairingCode returns a 6-character unambiguous uppercase code.
func newPairingCode() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no I,O,0,1
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return string(buf), nil
}

// chunk splits text into pieces no longer than max, preferring to break on a
// newline near the limit.
func chunk(text string, max int) []string {
	if len(text) <= max {
		return []string{text}
	}
	var out []string
	for len(text) > max {
		cut := max
		if nl := lastNewline(text[:max]); nl > max/2 {
			cut = nl
		}
		out = append(out, text[:cut])
		text = text[cut:]
	}
	if text != "" {
		out = append(out, text)
	}
	return out
}

func lastNewline(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\n' {
			return i
		}
	}
	return -1
}
