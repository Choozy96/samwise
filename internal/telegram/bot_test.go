package telegram

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"samwise/internal/store"
)

// TestAddressedInGroup checks the "is this group message directed at me?" logic.
func TestAddressedInGroup(t *testing.T) {
	bot := &Bot{username: "MyBot", selfID: 42}
	cases := []struct {
		name string
		msg  *Message
		text string
		want bool
	}{
		{"command", &Message{}, "/help", true},
		{"mention", &Message{}, "hey @mybot what's up", true},      // case-insensitive
		{"mention midword no", &Message{}, "email me@mybot", true}, // contains @mybot substring — acceptable
		{"reply to bot", &Message{ReplyToMessage: &Message{From: &User{ID: 42}}}, "thanks", true},
		{"plain chatter", &Message{}, "just talking to friends", false},
		{"reply to someone else", &Message{ReplyToMessage: &Message{From: &User{ID: 99}}}, "ok", false},
	}
	for _, c := range cases {
		if got := bot.addressedInGroup(c.msg, c.text); got != c.want {
			t.Errorf("%s: addressedInGroup=%v want %v", c.name, got, c.want)
		}
	}
}

// TestReplyContext checks that the quoted parent message is rendered for the
// agent (and omitted when there's nothing to quote).
func TestReplyContext(t *testing.T) {
	const selfID = 42

	// No reply → no context.
	if got := replyContext(&Message{Text: "hi"}, selfID); got != "" {
		t.Errorf("non-reply should yield no context, got %q", got)
	}

	// Reply to another user: quote their text and name.
	got := replyContext(&Message{
		Text:           "@MyBot summarize this",
		ReplyToMessage: &Message{Text: "The deploy is scheduled for Friday.", From: &User{ID: 99, FirstName: "Sam"}},
	}, selfID)
	if !strings.Contains(got, "Sam") || !strings.Contains(got, "deploy is scheduled for Friday") {
		t.Errorf("expected parent author and text in context, got %q", got)
	}
	if !strings.HasSuffix(got, "\n\n") {
		t.Errorf("context should end with a separator, got %q", got)
	}

	// Reply to the bot's own message: attribute it to the assistant.
	got = replyContext(&Message{
		Text:           "go on",
		ReplyToMessage: &Message{Text: "Here is the plan.", From: &User{ID: selfID}},
	}, selfID)
	if !strings.Contains(got, "you (the assistant)") || !strings.Contains(got, "Here is the plan.") {
		t.Errorf("reply to bot should attribute to the assistant, got %q", got)
	}

	// Reply to a caption-only parent (e.g. a photo): use the caption.
	got = replyContext(&Message{
		Text:           "what's this",
		ReplyToMessage: &Message{Caption: "Q3 revenue chart", From: &User{ID: 7, Username: "boss"}},
	}, selfID)
	if !strings.Contains(got, "Q3 revenue chart") || !strings.Contains(got, "@boss") {
		t.Errorf("caption parent should be quoted with @username, got %q", got)
	}

	// Reply to a bare sticker/photo with no text/caption → nothing to quote.
	if got := replyContext(&Message{Text: "lol", ReplyToMessage: &Message{From: &User{ID: 7}}}, selfID); got != "" {
		t.Errorf("textless parent should yield no context, got %q", got)
	}
}

// TestGroupShouldReply checks the per-user mode gate end-to-end through settings.
func TestGroupShouldReply(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	uid, _ := db.CreateUser(ctx, "alice", "h", false)
	bot := &Bot{db: db, username: "mybot", selfID: 42}

	// Default mode is "mention": plain chatter ignored, @mention answered.
	if bot.groupShouldReply(ctx, uid, &Message{}, "hello everyone") {
		t.Error("default (mention): plain chatter should be ignored")
	}
	if !bot.groupShouldReply(ctx, uid, &Message{}, "hi @mybot") {
		t.Error("default (mention): an @mention should be answered")
	}

	// Switch to "all": every message is answered.
	s, _ := db.GetSettings(ctx, uid)
	s.GroupReplyMode = "all"
	if err := db.UpdateSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	if !bot.groupShouldReply(ctx, uid, &Message{}, "hello everyone") {
		t.Error("all mode: plain chatter should be answered")
	}
}

// TestPairOnAddedToGroup confirms the bot posts a pairing code to a group the
// moment it's added (my_chat_member), only when the group isn't already paired,
// and stays silent when it's removed.
func TestPairOnAddedToGroup(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	uid, _ := db.CreateUser(ctx, "alice", "h", true)

	rb := newRecordingBot(t)
	bot := &Bot{client: rb.client(), db: db, log: slog.New(slog.NewTextHandler(io.Discard, nil)), botID: 0}

	added := func(chatID int64, status string) Update {
		return Update{MyChatMember: &ChatMemberUpdate{
			Chat:          &Chat{ID: chatID, Type: "supergroup"},
			NewChatMember: &ChatMember{Status: status},
		}}
	}
	resetCalls := func() { rb.mu.Lock(); rb.gotChat = 0; rb.gotCalls = 0; rb.mu.Unlock() }

	// Added to an unpaired group → a pairing code is posted to that group.
	bot.handle(ctx, added(-100777, "member"))
	if rb.chat() != -100777 {
		t.Errorf("added-to-group should post a pairing code to the group; got chat %d", rb.chat())
	}

	// Already-paired group → no re-prompt.
	_ = db.CreateIdentity(ctx, store.ChannelIdentity{UserID: uid, Channel: "telegram", BotID: 0, ExternalID: "-100888", ChatID: "-100888"})
	resetCalls()
	bot.handle(ctx, added(-100888, "administrator"))
	if rb.gotCalls != 0 {
		t.Errorf("already-paired group should not be re-prompted; got %d calls", rb.gotCalls)
	}

	// Removed from a group (left) → nothing posted.
	resetCalls()
	bot.handle(ctx, added(-100999, "left"))
	if rb.gotCalls != 0 {
		t.Errorf("leaving a group should not post anything; got %d calls", rb.gotCalls)
	}
}

// TestSenderKey verifies a DM is keyed by the sender's id while a group is keyed
// by the group's chat id (so the whole group pairs to one user).
func TestSenderKey(t *testing.T) {
	if got := senderKey(&Chat{ID: 555, Type: "private"}, &User{ID: 999}); got != "999" {
		t.Errorf("DM should use sender id: got %q want 999", got)
	}
	if got := senderKey(&Chat{ID: -1001234, Type: "supergroup"}, &User{ID: 999}); got != "-1001234" {
		t.Errorf("supergroup should use chat id: got %q want -1001234", got)
	}
	if got := senderKey(&Chat{ID: -42, Type: "group"}, &User{ID: 7}); got != "-42" {
		t.Errorf("group should use chat id: got %q want -42", got)
	}
	if !isGroup(&Chat{Type: "group"}) || !isGroup(&Chat{Type: "supergroup"}) {
		t.Error("group/supergroup should be detected as group")
	}
	if isGroup(&Chat{Type: "private"}) {
		t.Error("private should not be a group")
	}
}
