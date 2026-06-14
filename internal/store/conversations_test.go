package store

import (
	"context"
	"testing"
)

// TestConversationUnifiedAcrossChannels guards the cross-channel sync guarantee:
// web and Telegram resolve to one shared thread per (user, agent), messages from
// both surfaces land in the same transcript, and each keeps its source channel.
func TestConversationUnifiedAcrossChannels(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	uid, err := db.CreateUser(ctx, "alice", "h", true)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := db.GetActiveAgent(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}

	web, err := db.GetOrCreateConversation(ctx, uid, "web", agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	tg, err := db.GetOrCreateConversation(ctx, uid, "telegram", agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if web.ID != tg.ID {
		t.Fatalf("web and telegram should share one thread, got %d vs %d", web.ID, tg.ID)
	}

	if _, err := db.AddMessage(ctx, web.ID, uid, "web", "user", "hi from web"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddMessage(ctx, tg.ID, uid, "telegram", "user", "hi from telegram"); err != nil {
		t.Fatal(err)
	}

	msgs, err := db.RecentMessages(ctx, web.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected both messages in the unified thread, got %d", len(msgs))
	}
	if msgs[0].Channel != "web" || msgs[1].Channel != "telegram" {
		t.Errorf("source channel not preserved: %q, %q", msgs[0].Channel, msgs[1].Channel)
	}
}
