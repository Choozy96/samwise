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

// TestTaskConversationIsolation verifies scheduled-task runs use a thread separate
// from the interactive chat: the interactive lookup never returns the task thread,
// and a message in one never appears in the other. This is what stops a task from
// reading or polluting the conversation (and from merging its reply with a chat
// message that fires at the same moment).
func TestTaskConversationIsolation(t *testing.T) {
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

	interactive, err := db.GetOrCreateConversation(ctx, uid, "web", agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := db.GetOrCreateTaskConversation(ctx, uid, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if interactive.ID == task.ID {
		t.Fatal("task conversation must be a separate thread from the interactive one")
	}

	// Interactive lookup returns the interactive thread, even though the task
	// thread is newer (higher id) — i.e. it filters by kind.
	if again, _ := db.GetOrCreateConversation(ctx, uid, "web", agent.ID); again.ID != interactive.ID {
		t.Errorf("interactive lookup returned %d, want interactive thread %d", again.ID, interactive.ID)
	}
	// Task lookup is stable (reuses the same task thread).
	if t2, _ := db.GetOrCreateTaskConversation(ctx, uid, agent.ID); t2.ID != task.ID {
		t.Errorf("task lookup not stable: got %d, want %d", t2.ID, task.ID)
	}

	// A message in the task thread must not surface in the interactive one.
	if _, err := db.AddMessage(ctx, task.ID, uid, "web", "assistant", "task-only output"); err != nil {
		t.Fatal(err)
	}
	msgs, err := db.RecentMessages(ctx, interactive.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if m.Content == "task-only output" {
			t.Error("task output leaked into the interactive conversation")
		}
	}
}
