package web

import (
	"context"
	"io"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"samwise/internal/store"
)

// TestJobsTemplateRenders is a render smoke test: it executes the jobs template
// with the delivery controls populated (including a Telegram-targeted job and a
// chat hint), catching execution-time errors the parse-time check can't.
func TestJobsTemplateRenders(t *testing.T) {
	data := pageData{
		"Title":     "Cron jobs",
		"User":      nil,
		"TZ":        "UTC",
		"ChatHints": []chatHint{{ChatID: "555", Label: "DM"}, {ChatID: "-100200", Label: "group"}},
		"Jobs": []jobView{
			{Job: store.Job{ID: 1, Name: "Briefing", Type: "agent_run", ScheduleSpec: "daily@08:00"},
				Summary: "morning briefing", DeliverySel: "tg", DeliveryChat: "-100200", NextLocal: "Mon"},
			{Job: store.Job{ID: 2, Name: "Reminder", Type: "direct_message", ScheduleSpec: "once@x"},
				Summary: "ping"},
		},
	}
	if err := tmpl.ExecuteTemplate(io.Discard, "jobs", data); err != nil {
		t.Fatalf("jobs template render: %v", err)
	}
}

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return db
}

// TestResolveTGChat is the access-control guarantee for the cron delivery field:
// a user-entered Telegram id resolves to a stored target ONLY when it's a chat
// they're actually paired to — the bot is looked up server-side, and an
// unpaired/arbitrary id is rejected (never an arbitrary chat they typed in).
func TestResolveTGChat(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	uid, err := db.CreateUser(ctx, "alice", "h", false)
	if err != nil {
		t.Fatal(err)
	}
	// alice is paired to bot 7: her own DM 555 and group chat -100200.
	for _, id := range []store.ChannelIdentity{
		{UserID: uid, Channel: "telegram", BotID: 7, ExternalID: "555", ChatID: "555"},
		{UserID: uid, Channel: "telegram", BotID: 7, ExternalID: "-100200", ChatID: "-100200"},
	} {
		if err := db.CreateIdentity(ctx, id); err != nil {
			t.Fatal(err)
		}
	}

	s := &Server{db: db}
	r := httptest.NewRequest("GET", "/jobs", nil)

	cases := []struct {
		chat   string
		want   string
		wantOK bool
	}{
		{"555", "tg:7:555", true},         // owned DM → bot resolved
		{"-100200", "tg:7:-100200", true}, // owned group
		{"-100999", "", false},            // NOT a chat alice owns → rejected
		{"999", "", false},                // arbitrary injected id → rejected
		{"", "", false},                   // empty → rejected
	}
	for _, c := range cases {
		got, ok := s.resolveTGChat(r, uid, c.chat)
		if got != c.want || ok != c.wantOK {
			t.Errorf("resolveTGChat(%q) = (%q,%v), want (%q,%v)", c.chat, got, ok, c.want, c.wantOK)
		}
	}

	// A second user must not be able to deliver to alice's group, even with the
	// exact chat id — it isn't in their identities.
	bob, _ := db.CreateUser(ctx, "bob", "h", false)
	if got, ok := s.resolveTGChat(r, bob, "-100200"); ok || got != "" {
		t.Errorf("CROSS-USER: bob resolved alice's group: (%q,%v)", got, ok)
	}
}
