package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestTelegramBotsCRUD(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	uid, err := db.CreateUser(ctx, "alice", "h", true)
	if err != nil {
		t.Fatal(err)
	}
	agentID, err := db.CreateAgent(ctx, Agent{UserID: uid, Name: "work", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	b1, err := db.CreateTelegramBot(ctx, TelegramBot{UserID: uid, Label: "Personal", TokenEnc: "enc1", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	b2, err := db.CreateTelegramBot(ctx, TelegramBot{UserID: uid, Label: "Work", TokenEnc: "enc2", AgentID: agentID, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	bots, _ := db.ListTelegramBots(ctx, uid)
	if len(bots) != 2 {
		t.Fatalf("want 2 bots, got %d", len(bots))
	}

	// Binding lookup finds the agent-bound bot.
	bound, err := db.BotAgentBinding(ctx, uid, agentID)
	if err != nil || bound.ID != b2 {
		t.Fatalf("BotAgentBinding: got %+v err %v", bound, err)
	}

	// Disable b1 → it drops out of the enabled set the manager polls.
	if err := db.UpdateTelegramBot(ctx, uid, b1, "Personal", 0, false); err != nil {
		t.Fatal(err)
	}
	enabled, _ := db.ListEnabledTelegramBots(ctx)
	if len(enabled) != 1 || enabled[0].ID != b2 {
		t.Fatalf("enabled set after disabling b1: %+v", enabled)
	}

	// Username caching + token replacement (replacing the token clears username).
	if err := db.SetTelegramBotUsername(ctx, b2, "work_bot"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTelegramBotToken(ctx, uid, b2, "enc2b"); err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetTelegramBot(ctx, uid, b2)
	if got.TokenEnc != "enc2b" || got.Username != "" {
		t.Fatalf("token/username after replace: %+v", got)
	}

	// Delete cascades the bot's pairings.
	if err := db.CreateIdentity(ctx, ChannelIdentity{UserID: uid, Channel: "telegram", BotID: b2, ExternalID: "999", ChatID: "5"}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteTelegramBot(ctx, uid, b2); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetIdentityByExternal(ctx, "telegram", b2, "999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("identity should be gone after bot delete, got %v", err)
	}
	if bots, _ := db.ListTelegramBots(ctx, uid); len(bots) != 1 {
		t.Fatalf("want 1 bot after delete, got %d", len(bots))
	}
}

// TestDeleteIdentity verifies unpairing removes only the targeted chat, leaving
// the bot's other paired chats (and other bots) untouched.
func TestDeleteIdentity(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	uid, _ := db.CreateUser(ctx, "alice", "h", true)

	// Two chats paired to the same bot, plus a different bot.
	_ = db.CreateIdentity(ctx, ChannelIdentity{UserID: uid, Channel: "telegram", BotID: 0, ExternalID: "1", ChatID: "10"})
	_ = db.CreateIdentity(ctx, ChannelIdentity{UserID: uid, Channel: "telegram", BotID: 0, ExternalID: "2", ChatID: "11"})
	_ = db.CreateIdentity(ctx, ChannelIdentity{UserID: uid, Channel: "telegram", BotID: 5, ExternalID: "1", ChatID: "20"})

	if err := db.DeleteIdentity(ctx, uid, "telegram", 0, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetIdentityByExternal(ctx, "telegram", 0, "1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bot 0 chat 1 should be gone, got %v", err)
	}
	// The same bot's other chat survives.
	if _, err := db.GetIdentityByExternal(ctx, "telegram", 0, "2"); err != nil {
		t.Fatalf("bot 0 chat 2 should remain: %v", err)
	}
	// The other bot's pairing is untouched.
	if _, err := db.GetIdentityByExternal(ctx, "telegram", 5, "1"); err != nil {
		t.Fatalf("bot 5 identity should remain: %v", err)
	}
}

// TestRebindAgent verifies a bot's agent binding can be changed at will (and
// cleared), which the Manager turns into a poller restart.
func TestRebindAgent(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	uid, _ := db.CreateUser(ctx, "alice", "h", true)
	a1, _ := db.CreateAgent(ctx, Agent{UserID: uid, Name: "work", Enabled: true})
	a2, _ := db.CreateAgent(ctx, Agent{UserID: uid, Name: "personal", Enabled: true})

	id, _ := db.CreateTelegramBot(ctx, TelegramBot{UserID: uid, Label: "Bot", TokenEnc: "e", AgentID: a1, Enabled: true})

	// Rebind a1 -> a2.
	if err := db.UpdateTelegramBot(ctx, uid, id, "Bot", a2, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetTelegramBot(ctx, uid, id); got.AgentID != a2 {
		t.Fatalf("rebind failed: agent_id=%d want %d", got.AgentID, a2)
	}
	if bound, _ := db.BotAgentBinding(ctx, uid, a2); bound == nil || bound.ID != id {
		t.Fatalf("BotAgentBinding didn't follow rebind to a2")
	}

	// Unbind (0) -> follows active agent.
	if err := db.UpdateTelegramBot(ctx, uid, id, "Bot", 0, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetTelegramBot(ctx, uid, id); got.AgentID != 0 {
		t.Fatalf("unbind failed: agent_id=%d want 0", got.AgentID)
	}
}

// TestIdentityBotScoping verifies the widened UNIQUE: the same Telegram account
// (external_id) can pair to two different bots, mapping to different users.
func TestIdentityBotScoping(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	u1, _ := db.CreateUser(ctx, "u1", "h", false)
	u2, _ := db.CreateUser(ctx, "u2", "h", false)

	const ext = "555"
	if err := db.CreateIdentity(ctx, ChannelIdentity{UserID: u1, Channel: "telegram", BotID: 10, ExternalID: ext, ChatID: "100"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateIdentity(ctx, ChannelIdentity{UserID: u2, Channel: "telegram", BotID: 20, ExternalID: ext, ChatID: "200"}); err != nil {
		t.Fatal(err)
	}

	a, err := db.GetIdentityByExternal(ctx, "telegram", 10, ext)
	if err != nil || a.UserID != u1 {
		t.Fatalf("bot 10 should map to u1: %+v err %v", a, err)
	}
	b, err := db.GetIdentityByExternal(ctx, "telegram", 20, ext)
	if err != nil || b.UserID != u2 {
		t.Fatalf("bot 20 should map to u2: %+v err %v", b, err)
	}
}

// TestPairingBotScoping verifies a pairing code carries its bot id through
// consume.
func TestPairingBotScoping(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := db.UpsertPairingCode(ctx, "ABC123", "telegram", 42, "777", "888", "2999-01-01 00:00:00"); err != nil {
		t.Fatal(err)
	}
	pc, err := db.ConsumePairingCode(ctx, "ABC123")
	if err != nil {
		t.Fatal(err)
	}
	if pc.BotID != 42 || pc.ExternalID != "777" {
		t.Fatalf("pairing code lost bot scoping: %+v", pc)
	}
}
