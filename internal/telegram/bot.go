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
	client   *Client
	db       *store.DB
	orch     *orchestrator.Orchestrator
	log      *slog.Logger
	botID    int64
	agentID  int64
	username string // the bot's @username, for detecting group @mentions
	selfID   int64  // the bot's Telegram user id, for detecting replies to it
}

// NewBot constructs the inbound bot. botID 0 and agentID 0 reproduce the legacy
// single-bot, active-agent behavior. username/selfID (from getMe) let it detect
// when a group message is addressed to it.
func NewBot(client *Client, db *store.DB, orch *orchestrator.Orchestrator, log *slog.Logger, botID, agentID, selfID int64, username string) *Bot {
	return &Bot{client: client, db: db, orch: orch, log: log, botID: botID, agentID: agentID, selfID: selfID, username: username}
}

// Run polls for updates until ctx is cancelled. Inbound long-polling reconnects
// automatically on error.
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
	// The bot being added to a group fires a my_chat_member update — pair the
	// group proactively so the owner gets a code without anyone having to message
	// it first.
	if u.MyChatMember != nil {
		b.handleMembership(ctx, u.MyChatMember)
		return
	}
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
	// In a group, the whole group is paired by its chat id (any member then talks
	// to the paired user's assistant — shared context is intended). In a 1:1 DM the
	// sender's id is the key.
	externalID := senderKey(msg.Chat, msg.From)

	ident, err := b.db.GetIdentityByExternal(ctx, channel, b.botID, externalID)
	if errors.Is(err, store.ErrNotFound) {
		b.handleUnpaired(ctx, externalID, msg.Chat.ID, isGroup(msg.Chat))
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

	// In a group, respect the user's reply mode: by default only respond when the
	// bot is addressed (@mention, a reply to it, or a command); "all" responds to
	// every message. (Requires the bot's Telegram privacy mode off to even receive
	// non-addressed messages.)
	if isGroup(msg.Chat) && !b.groupShouldReply(ctx, user.ID, msg, text) {
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

	// In a group, only members whose own Telegram account is registered with the
	// assistant may perform write operations; everyone else can chat/read but the
	// run is read-only (the agent acts as the group's owner, so we don't let a
	// stranger mutate the owner's memory/jobs or run write-capable tools). Computed
	// here because it also gates slash commands below.
	readOnly := false
	if isGroup(msg.Chat) && msg.From != nil {
		if paired, perr := b.db.TelegramUserIsPaired(ctx, msg.From.ID); perr == nil && !paired {
			readOnly = true
		}
	}

	// Slash commands (/model, /runtime, /status, /help) work over Telegram too.
	// commandForChat handles the addressing: in a group it's honoured ONLY when
	// this bot is explicitly mentioned ("@thisbot /cmd", "/cmd … @thisbot", or
	// "/cmd@thisbot"); a bare "/cmd" is ignored. The "@bot" mention is stripped so
	// the parser sees "/cmd". (Not pre-gated on a leading "/", since a leading
	// mention puts "@bot" first.)
	{
		if cmdText, forUs := b.commandForChat(msg.Chat, text); forUs {
			// Commands mutate the (shared, owner) account — model, runtime, delivery,
			// memory resets, etc. In a group an unregistered sender may not run them.
			if readOnly {
				_ = b.send(ctx, msg.Chat.ID, "🔒 Only registered users can run commands in a group. "+
					"Pair your own Telegram account with the assistant first (DM the bot).")
				return
			}
			// A bound bot always speaks as its agent, so /agent switching here would
			// be misleading — explain instead of changing the (unused) active agent.
			if boundAgent != nil && isAgentSwitch(cmdText) {
				_ = b.send(ctx, msg.Chat.ID, "This bot is bound to the “"+boundAgent.Name+"” agent, so /agent doesn’t apply here. "+
					"Use the web portal to switch the agent a bot is bound to.")
				return
			}
			if reply, handled := b.orch.TryCommand(ctx, user.ID, cmdText); handled {
				_ = b.send(ctx, msg.Chat.ID, reply)
				return
			}
		}
	}

	// Show a "typing…" indicator while the agent works (refreshed on a loop,
	// since each action lasts only ~5s).
	typingCtx, stopTyping := context.WithCancel(ctx)
	go b.keepTyping(typingCtx, msg.Chat.ID)

	// Download any attached file(s) into the user's workspace so the agent's
	// tools can read them.
	atts := b.downloadAttachments(ctx, user.ID, msg)

	// When this message replies to another (the common "tag the bot in a reply
	// chain" case), the user's text alone is contextless — the agent can't see
	// the message being pointed at. Prepend the quoted parent so it can.
	dispatchText := text
	if rc := replyContext(msg, b.selfID); rc != "" {
		dispatchText = rc + text
	}

	// Dispatch to this bot's bound agent (boundAgent), or the user's active agent
	// when the bot is unbound (boundAgent == nil).
	res, err := b.orch.Dispatch(ctx, orchestrator.DispatchRequest{
		User:             user,
		Channel:          channel,
		Agent:            boundAgent,
		UserMessage:      dispatchText,
		Attachments:      atts,
		StoreUserMessage: true,
		ReadOnly:         readOnly,
		OriginBotID:      b.botID,
		OriginChatID:     msg.Chat.ID,
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

// handleMembership reacts to the bot's membership changing. When it's added to
// (or promoted in) a group that isn't paired yet, it issues a pairing code to the
// group so the owner can link it from the portal.
func (b *Bot) handleMembership(ctx context.Context, m *ChatMemberUpdate) {
	if m.Chat == nil || !isGroup(m.Chat) || m.NewChatMember == nil {
		return
	}
	if s := m.NewChatMember.Status; s != "member" && s != "administrator" {
		return // left/kicked/restricted — nothing to do
	}
	externalID := strconv.FormatInt(m.Chat.ID, 10)
	// Don't re-prompt a group that's already linked.
	if _, err := b.db.GetIdentityByExternal(ctx, channel, b.botID, externalID); err == nil {
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		b.log.Error("telegram membership identity lookup", "err", err)
		return
	}
	b.handleUnpaired(ctx, externalID, m.Chat.ID, true)
}

// senderKey returns the pairing/identity key for a message: the group's chat id
// for a group chat (so the whole group maps to one user), else the sender's id.
func senderKey(chat *Chat, from *User) string {
	if isGroup(chat) {
		return strconv.FormatInt(chat.ID, 10)
	}
	return strconv.FormatInt(from.ID, 10)
}

// isGroup reports whether a chat is a Telegram group or supergroup.
func isGroup(chat *Chat) bool {
	return chat != nil && (chat.Type == "group" || chat.Type == "supergroup")
}

// groupShouldReply decides whether to act on a group message given the paired
// user's reply mode. "all" always replies; "mention" (default) replies only when
// the message is addressed to the bot.
func (b *Bot) groupShouldReply(ctx context.Context, userID int64, msg *Message, text string) bool {
	mode := "mention"
	if s, err := b.db.GetSettings(ctx, userID); err == nil && s.GroupReplyMode != "" {
		mode = s.GroupReplyMode
	}
	if mode == "all" {
		return true
	}
	return b.addressedInGroup(msg, text)
}

// addressedInGroup reports whether a group message is directed at this bot: a
// command that mentions this bot (leading/trailing/native), a reply to one of the
// bot's messages, or an @mention of it.
func (b *Bot) addressedInGroup(msg *Message, text string) bool {
	// A command addresses us only when this bot is explicitly mentioned; a bare
	// "/cmd" (commandForChat → forUs=false in a group) falls through to the
	// @mention/reply checks below (and isn't an @mention either, so it's ignored).
	if _, forUs := b.commandForChat(msg.Chat, text); forUs {
		return true
	}
	t := strings.TrimSpace(text)
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil &&
		b.selfID != 0 && msg.ReplyToMessage.From.ID == b.selfID {
		return true
	}
	if b.username != "" && strings.Contains(strings.ToLower(t), "@"+strings.ToLower(b.username)) {
		return true
	}
	return false
}

// commandForChat decides whether a message should be handled as a command for
// THIS bot, and returns the command text with any "@thisbot" addressing stripped
// (so the parser sees a bare "/cmd …"). Returns forUs=false (and is a no-op) for
// anything that isn't a command. This bot can be addressed three ways, all
// equivalent:
//
//   - leading mention:  "@thisbot /cmd args"
//   - trailing mention: "/cmd args @thisbot"
//   - native target:    "/cmd@thisbot args"  (what Telegram's command menu inserts)
//
// Rules:
//   - DM: a clean "/cmd …" is always ours (there's only one bot in the chat); a
//     mention is allowed but not required.
//   - Group: ONLY when this bot is explicitly mentioned (any of the three forms
//     above). A bare, untargeted "/cmd" in a group is NOT a command for us (it's
//     ignored, not executed) — this stops the bot acting on stray slashes and
//     forces a deliberate mention.
//
// "@anotherbot" (native target on the command) is never ours, in any chat.
func (b *Bot) commandForChat(chat *Chat, text string) (stripped string, forUs bool) {
	t := strings.TrimSpace(text)

	mentioned := false     // this bot explicitly addressed
	targetedOther := false // "/cmd@otherbot"

	// Leading "@thisbot" mention ("@thisbot /cmd …"): strip it, if it's a whole
	// token, before looking for the command.
	if b.username != "" {
		lead := "@" + b.username
		if len(t) >= len(lead) && strings.EqualFold(t[:len(lead)], lead) {
			if rest := t[len(lead):]; rest == "" || rest[0] == ' ' {
				mentioned = true
				t = strings.TrimSpace(rest)
			}
		}
	}

	if !strings.HasPrefix(t, "/") {
		return strings.TrimSpace(text), false // not a command
	}
	fields := strings.Fields(t)
	cmd := fields[0]
	args := fields[1:]

	// Native target on the command token: "/cmd@bot".
	if at := strings.IndexByte(cmd, '@'); at >= 0 {
		target := cmd[at+1:]
		cmd = cmd[:at]
		if b.username != "" && strings.EqualFold(target, b.username) {
			mentioned = true
		} else {
			targetedOther = true
		}
	}
	// Trailing/standalone "@thisbot" mention token(s); drop them from the args so
	// the parser doesn't see the mention as an argument.
	kept := args[:0]
	for _, a := range args {
		if b.username != "" && strings.EqualFold(a, "@"+b.username) {
			mentioned = true
			continue
		}
		kept = append(kept, a)
	}
	stripped = cmd
	if len(kept) > 0 {
		stripped += " " + strings.Join(kept, " ")
	}

	if isGroup(chat) {
		forUs = mentioned && !targetedOther
	} else {
		forUs = !targetedOther
	}
	return stripped, forUs
}

// replyContext renders a quoted block describing the message this one replies to,
// so the agent can "see" what it was tagged on in a reply chain. Telegram only
// exposes the immediate parent (not the whole chain), which is what we quote.
// Returns "" when there's no reply or the parent carries no quotable text.
func replyContext(msg *Message, selfID int64) string {
	rt := msg.ReplyToMessage
	if rt == nil {
		return ""
	}
	quoted := strings.TrimSpace(rt.Text)
	if quoted == "" {
		quoted = strings.TrimSpace(rt.Caption)
	}
	if quoted == "" {
		return "" // a reply to a bare photo/sticker/etc. — nothing to quote
	}
	who := "someone"
	if rt.From != nil {
		switch {
		case selfID != 0 && rt.From.ID == selfID:
			who = "you (the assistant)"
		case rt.From.FirstName != "":
			who = rt.From.FirstName
		case rt.From.Username != "":
			who = "@" + rt.From.Username
		}
	}
	return fmt.Sprintf("[Replying to a message from %s:\n%s]\n\n", who, quoted)
}

// handleUnpaired issues a pairing code to an unknown sender or group.
// This is the only reply an unpaired sender gets; it lets the owner link the chat
// from the portal.
func (b *Bot) handleUnpaired(ctx context.Context, externalID string, chatID int64, group bool) {
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
	b.log.Info("telegram pairing code issued", "external_id", externalID, "group", group)
	what := "this chat"
	if group {
		what = "this group"
	}
	_ = b.send(ctx, chatID, fmt.Sprintf(
		"👋 To connect %s to your assistant, log into the web portal, open the Agents page, and enter this code within 15 minutes:\n\n%s",
		what, code))
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
