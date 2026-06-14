package telegram

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"samwise/internal/store"
)

// recordingBot is an httptest server standing in for one bot's Telegram API; it
// records the chat_id of the last sendMessage it received.
type recordingBot struct {
	srv      *httptest.Server
	mu       sync.Mutex
	gotChat  int64
	gotCalls int
}

func newRecordingBot(t *testing.T) *recordingBot {
	t.Helper()
	rb := &recordingBot{}
	rb.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bott/sendMessage" {
			// getMe / sendChatAction etc. — just succeed.
			io.WriteString(w, `{"ok":true,"result":{}}`)
			return
		}
		var body struct {
			ChatID int64 `json:"chat_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		rb.mu.Lock()
		rb.gotChat = body.ChatID
		rb.gotCalls++
		rb.mu.Unlock()
		io.WriteString(w, `{"ok":true,"result":{}}`)
	}))
	t.Cleanup(rb.srv.Close)
	return rb
}

func (rb *recordingBot) client() *Client {
	return &Client{token: "t", baseURL: rb.srv.URL + "/bott", http: rb.srv.Client()}
}

func (rb *recordingBot) chat() int64 {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.gotChat
}

// TestManagerRouting checks that delivery picks the right bot: SendAgent routes
// to the bot bound to the agent, SendBot to the named bot, and Send to the
// primary (lowest bot_id) paired bot.
func TestManagerRouting(t *testing.T) {
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
	workAgent, _ := db.CreateAgent(ctx, store.Agent{UserID: uid, Name: "work", Enabled: true})

	b1, _ := db.CreateTelegramBot(ctx, store.TelegramBot{UserID: uid, Label: "Personal", TokenEnc: "e1", Enabled: true})
	b2, _ := db.CreateTelegramBot(ctx, store.TelegramBot{UserID: uid, Label: "Work", TokenEnc: "e2", AgentID: workAgent, Enabled: true})

	// Pair the user to each bot at distinct chats.
	_ = db.CreateIdentity(ctx, store.ChannelIdentity{UserID: uid, Channel: "telegram", BotID: b1, ExternalID: "1", ChatID: "1001"})
	_ = db.CreateIdentity(ctx, store.ChannelIdentity{UserID: uid, Channel: "telegram", BotID: b2, ExternalID: "2", ChatID: "2002"})

	rb1, rb2 := newRecordingBot(t), newRecordingBot(t)
	m := &Manager{
		db:  db,
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		running: map[int64]*botHandle{
			b1: {client: rb1.client(), cancel: func() {}, fingerprint: "f1"},
			b2: {client: rb2.client(), cancel: func() {}, fingerprint: "f2"},
		},
	}

	// SendAgent(work) → bot bound to work agent (b2 / chat 2002).
	if err := m.SendAgent(ctx, uid, workAgent, "hi work"); err != nil {
		t.Fatalf("SendAgent: %v", err)
	}
	if rb2.chat() != 2002 {
		t.Errorf("SendAgent should hit b2 chat 2002, got %d", rb2.chat())
	}
	if rb1.gotCalls != 0 {
		t.Errorf("SendAgent should not hit b1, got %d calls", rb1.gotCalls)
	}

	// SendBot(b1) → that specific bot (chat 1001).
	if err := m.SendBot(ctx, uid, b1, "direct"); err != nil {
		t.Fatalf("SendBot: %v", err)
	}
	if rb1.chat() != 1001 {
		t.Errorf("SendBot b1 should hit chat 1001, got %d", rb1.chat())
	}

	// Send → primary (lowest bot_id paired = b1 / chat 1001).
	rb1.mu.Lock()
	rb1.gotChat = 0
	rb1.mu.Unlock()
	if err := m.Send(ctx, uid, "yo"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if rb1.chat() != 1001 {
		t.Errorf("Send (primary) should hit b1 chat 1001, got %d", rb1.chat())
	}
}
