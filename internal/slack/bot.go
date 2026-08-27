package slack

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/slack-go/slack/slackevents"

	"samwise/internal/orchestrator"
	"samwise/internal/store"
)

const (
	channel    = "slack"
	pairingTTL = 15 * time.Minute
)

// Bot handles one Slack app's inbound events (the Manager owns the Socket Mode
// connection and routes events here). botID is its slack_bots row id; agentID,
// when non-zero, binds every message on this app to that agent.
type Bot struct {
	client  *Client
	db      *store.DB
	orch    *orchestrator.Orchestrator
	log     *slog.Logger
	botID   int64
	agentID int64
	selfID  string // the app's bot user id (U…), for mention detection
}

// NewBot constructs the event handler for one app.
func NewBot(client *Client, db *store.DB, orch *orchestrator.Orchestrator, log *slog.Logger, botID, agentID int64, selfID string) *Bot {
	return &Bot{client: client, db: db, orch: orch, log: log, botID: botID, agentID: agentID, selfID: selfID}
}

// fileRef is one attached file from a message event's raw payload
// (slackevents doesn't parse "files" on plain message events).
type fileRef struct {
	Name string
	URL  string // url_private_download (falls back to url_private)
}

// extractFiles pulls attached-file refs out of a raw Events API payload.
func extractFiles(payload []byte) []fileRef {
	var env struct {
		Event struct {
			Files []struct {
				Name               string `json:"name"`
				URLPrivate         string `json:"url_private"`
				URLPrivateDownload string `json:"url_private_download"`
			} `json:"files"`
		} `json:"event"`
	}
	if json.Unmarshal(payload, &env) != nil {
		return nil
	}
	var out []fileRef
	for _, f := range env.Event.Files {
		url := f.URLPrivateDownload
		if url == "" {
			url = f.URLPrivate
		}
		if url == "" {
			continue
		}
		name := f.Name
		if name == "" {
			name = "file"
		}
		out = append(out, fileRef{Name: name, URL: url})
	}
	return out
}

// mentionsBot reports whether text mentions the app's bot user.
func mentionsBot(text, selfID string) bool {
	return selfID != "" && strings.Contains(text, "<@"+selfID+">")
}

// stripMention removes the app's own mention token(s) from text.
func stripMention(text, selfID string) string {
	if selfID == "" {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(strings.ReplaceAll(text, "<@"+selfID+">", ""))
}

// replyThreadTS picks where to reply: in a channel, thread under the message
// (its existing thread if any, else the message itself — keeps channels tidy);
// in a DM, no thread.
func replyThreadTS(isDM bool, threadTS, ts string) string {
	if isDM {
		return ""
	}
	if threadTS != "" {
		return threadTS
	}
	return ts
}

// shouldHandleChannelMessage is the dedup rule for non-DM message events: a
// mention arrives as BOTH app_mention and message, so mention-bearing messages
// are left to HandleMention; un-mentioned channel messages are handled only in
// reply-to-everything mode.
func shouldHandleChannelMessage(text, selfID, groupReplyMode string) bool {
	if mentionsBot(text, selfID) {
		return false // app_mention covers it
	}
	return groupReplyMode == "all"
}

// HandleEvent routes one Events API event (raw payload alongside for files).
func (b *Bot) HandleEvent(ctx context.Context, e slackevents.EventsAPIEvent, payload []byte) {
	switch ev := e.InnerEvent.Data.(type) {
	case *slackevents.MessageEvent:
		b.handleMessage(ctx, ev, extractFiles(payload))
	case *slackevents.AppMentionEvent:
		b.handleMention(ctx, ev, extractFiles(payload))
	}
}

// handleMention: the bot was @mentioned in a channel.
func (b *Bot) handleMention(ctx context.Context, ev *slackevents.AppMentionEvent, files []fileRef) {
	if ev.BotID != "" || ev.User == "" || ev.User == b.selfID {
		return // a bot (possibly us) — never converse with bots
	}
	ptext, pfiles := b.threadParent(ctx, ev.Channel, ev.ThreadTimeStamp, ev.TimeStamp)
	b.process(ctx, inbound{
		sender:     ev.User,
		chatID:     ev.Channel,
		group:      true,
		threadTS:   replyThreadTS(false, ev.ThreadTimeStamp, ev.TimeStamp),
		text:       stripMention(ev.Text, b.selfID),
		files:      append(files, pfiles...),
		parentText: ptext,
	})
}

// handleMessage: a DM, or a channel message in reply-to-everything mode.
func (b *Bot) handleMessage(ctx context.Context, ev *slackevents.MessageEvent, files []fileRef) {
	if ev.BotID != "" || ev.User == "" || ev.User == b.selfID {
		return // our own replies / other bots
	}
	if ev.SubType != "" && ev.SubType != "file_share" {
		return // edits, deletions, joins, etc.
	}
	isDM := ev.ChannelType == "im"
	if !isDM && !shouldHandleChannelMessage(ev.Text, b.selfID, b.groupReplyMode(ctx, ev.Channel)) {
		return
	}
	ptext, pfiles := b.threadParent(ctx, ev.Channel, ev.ThreadTimeStamp, ev.TimeStamp)
	b.process(ctx, inbound{
		sender:     ev.User,
		chatID:     ev.Channel,
		group:      !isDM,
		threadTS:   replyThreadTS(isDM, ev.ThreadTimeStamp, ev.TimeStamp),
		text:       stripMention(ev.Text, b.selfID),
		files:      append(files, pfiles...),
		parentText: ptext,
	})
}

// inbound is a normalized incoming message.
type inbound struct {
	sender     string // Slack user id (U…)
	chatID     string // conversation id (D… for DMs, C…/G… for channels)
	group      bool
	threadTS   string
	text       string
	files      []fileRef
	parentText string // thread-root text, quoted for context when replying in a thread
}

// inThread reports whether a message is a REPLY inside a thread (not the root).
func inThread(threadTS, ts string) bool {
	return threadTS != "" && threadTS != ts
}

// threadParent fetches the thread root's text + files when the message is an
// in-thread reply — so "@bot summarize this" on a file post sees the file.
func (b *Bot) threadParent(ctx context.Context, channelID, threadTS, ts string) (string, []fileRef) {
	if !inThread(threadTS, ts) {
		return "", nil
	}
	text, files, err := b.client.ThreadParent(ctx, channelID, threadTS)
	if err != nil {
		b.log.Warn("slack thread parent fetch", "err", err)
		return "", nil
	}
	if len(files) > 0 {
		b.log.Info("slack: attached media from thread parent", "files", len(files))
	}
	return text, files
}

// groupReplyMode reads the paired owner's group-reply preference for a channel
// ("mention" default). Unpaired channels default to mention-only.
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

// process runs the shared pipeline: pairing, gating, commands, attachments,
// dispatch, reply — the Slack mirror of telegram's handle().
func (b *Bot) process(ctx context.Context, in inbound) {
	if in.text == "" && len(in.files) == 0 {
		return
	}

	// Quote the thread root so the agent sees what the reply points at (the
	// Telegram replyContext analog).
	if in.parentText != "" && in.text != "" {
		in.text = "[In reply to an earlier message: " + snippet(in.parentText, 1500) + "]\n\n" + in.text
	}

	// Identity: a DM pairs the SENDER (external = U…); a channel pairs the
	// CHANNEL (external = C…) — shared context, like Telegram groups.
	externalID := in.sender
	if in.group {
		externalID = in.chatID
	}
	ident, err := b.db.GetIdentityByExternal(ctx, channel, b.botID, externalID)
	if errors.Is(err, store.ErrNotFound) {
		b.handleUnpaired(ctx, externalID, in.chatID, in.threadTS, in.group)
		return
	}
	if err != nil {
		b.log.Error("slack identity lookup", "err", err)
		return
	}
	user, err := b.db.GetUserByID(ctx, ident.UserID)
	if err != nil || user.Disabled {
		return
	}

	// Bound agent (user-scoped lookup; falls through to active agent).
	var boundAgent *store.Agent
	if b.agentID != 0 {
		if a, aerr := b.db.GetAgent(ctx, user.ID, b.agentID); aerr == nil {
			boundAgent = a
		}
	}

	// In a channel, only members whose own Slack account is paired may perform
	// writes; everyone else chats read-only (same rule as Telegram groups).
	readOnly := false
	if in.group {
		if paired, perr := b.db.UserPairedOnChannel(ctx, channel, in.sender); perr == nil && !paired {
			readOnly = true
		}
	}

	// Slash commands work on Slack too (in channels they arrive via @mention,
	// so addressing is already established).
	if strings.HasPrefix(in.text, "/") {
		if readOnly {
			b.send(ctx, in.chatID, in.threadTS, "🔒 Only registered users can run commands in a channel. Pair your own Slack account with the assistant first (DM the app).")
			return
		}
		if reply, handled := b.orch.TryCommand(ctx, user.ID, in.text); handled {
			b.send(ctx, in.chatID, in.threadTS, reply)
			return
		}
	}

	// Download attachments into the user's workspace.
	var atts []orchestrator.Attachment
	for _, f := range in.files {
		data, derr := b.client.DownloadFile(ctx, f.URL)
		if derr != nil {
			b.log.Warn("slack file download", "name", f.Name, "err", derr)
			continue
		}
		att, aerr := b.orch.SaveAttachment(user.ID, f.Name, data)
		if aerr != nil {
			b.log.Warn("slack file save", "name", f.Name, "err", aerr)
			continue
		}
		atts = append(atts, att)
	}

	res, err := b.orch.Dispatch(ctx, orchestrator.DispatchRequest{
		User:             user,
		Channel:          channel,
		Agent:            boundAgent,
		UserMessage:      in.text,
		Attachments:      atts,
		StoreUserMessage: true,
		ReadOnly:         readOnly,
		Origin:           orchestrator.Address{Channel: channel, BotID: b.botID, ChatID: in.chatID},
	}, nil)
	if err != nil {
		b.log.Error("slack dispatch", "user_id", user.ID, "err", err)
		b.send(ctx, in.chatID, in.threadTS, "Something went wrong handling that — try again in a moment.")
		return
	}
	if res != nil && strings.TrimSpace(res.FinalText) != "" {
		b.send(ctx, in.chatID, in.threadTS, res.FinalText)
	}
}

// handleUnpaired issues a pairing code (DM: for the sender; channel: for the
// channel) — same flow as Telegram.
func (b *Bot) handleUnpaired(ctx context.Context, externalID, chatID, threadTS string, group bool) {
	code, err := newPairingCode()
	if err != nil {
		b.log.Error("slack pairing code gen", "err", err)
		return
	}
	expires := time.Now().Add(pairingTTL).UTC().Format("2006-01-02 15:04:05")
	if err := b.db.UpsertPairingCode(ctx, code, channel, b.botID, externalID, chatID, expires); err != nil {
		b.log.Error("slack pairing code store", "err", err)
		return
	}
	b.log.Info("slack pairing code issued", "external_id", externalID, "group", group)
	what := "this chat"
	if group {
		what = "this channel"
	}
	b.send(ctx, chatID, threadTS, fmt.Sprintf(
		"👋 To connect %s to your assistant, log into the web portal, open the Agents page, and enter this code within 15 minutes:\n\n%s",
		what, code))
}

// send delivers formatted text (mrkdwn) to a chat, best-effort with logging.
func (b *Bot) send(ctx context.Context, chatID, threadTS, text string) {
	if err := deliver(ctx, b.client, chatID, threadTS, text, b.log); err != nil {
		b.log.Warn("slack send failed", "chat", chatID, "err", err)
	}
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
