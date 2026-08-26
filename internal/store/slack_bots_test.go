package store

import (
	"context"
	"errors"
	"testing"
)

// TestSlackBotCRUD covers the slack_bots DAL end-to-end: create/get/list,
// identity caching, token replacement clearing the cache, agent binding, and
// delete sweeping identities + pairing codes.
func TestSlackBotCRUD(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	uid, err := db.CreateUser(ctx, "alice", "h", true)
	if err != nil {
		t.Fatal(err)
	}
	agentID, err := db.CreateAgent(ctx, Agent{UserID: uid, Name: "Coach", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	id, err := db.CreateSlackBot(ctx, SlackBot{
		UserID: uid, Label: "Work", BotTokenEnc: "enc-bot", AppTokenEnc: "enc-app",
		AgentID: agentID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.GetSlackBot(ctx, uid, id)
	if err != nil || b.Label != "Work" || b.BotTokenEnc != "enc-bot" || b.AppTokenEnc != "enc-app" || b.AgentID != agentID {
		t.Fatalf("get: %+v err=%v", b, err)
	}

	// Identity caching + token replacement clears it.
	if err := db.SetSlackBotIdentity(ctx, id, "U0123", "Acme"); err != nil {
		t.Fatal(err)
	}
	b, _ = db.GetSlackBot(ctx, uid, id)
	if b.BotUserID != "U0123" || b.TeamName != "Acme" {
		t.Fatalf("identity cache: %+v", b)
	}
	if err := db.UpdateSlackBotTokens(ctx, uid, id, "enc-bot2", "enc-app2"); err != nil {
		t.Fatal(err)
	}
	b, _ = db.GetSlackBot(ctx, uid, id)
	if b.BotUserID != "" || b.BotTokenEnc != "enc-bot2" {
		t.Fatalf("token replace should clear cached identity: %+v", b)
	}

	// Enabled listing + agent binding.
	if bots, _ := db.ListEnabledSlackBots(ctx); len(bots) != 1 {
		t.Fatalf("enabled list: %d", len(bots))
	}
	if err := db.UpdateSlackBot(ctx, uid, id, "Work2", agentID, false); err != nil {
		t.Fatal(err)
	}
	if bots, _ := db.ListEnabledSlackBots(ctx); len(bots) != 0 {
		t.Fatal("disabled bot still listed as enabled")
	}
	if _, err := db.SlackBotAgentBinding(ctx, uid, agentID); !errors.Is(err, ErrNotFound) {
		t.Fatal("disabled bot should not bind")
	}
	_ = db.UpdateSlackBot(ctx, uid, id, "Work2", agentID, true)
	if bb, err := db.SlackBotAgentBinding(ctx, uid, agentID); err != nil || bb.ID != id {
		t.Fatalf("binding: %+v err=%v", bb, err)
	}

	// Delete sweeps slack identities + pairing codes for the bot.
	if err := db.CreateIdentity(ctx, ChannelIdentity{UserID: uid, Channel: "slack", BotID: id, ExternalID: "U9", ChatID: "D9"}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteSlackBot(ctx, uid, id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetSlackBot(ctx, uid, id); !errors.Is(err, ErrNotFound) {
		t.Fatal("bot should be gone")
	}
	if ids, _ := db.ListIdentitiesByUser(ctx, uid, "slack"); len(ids) != 0 {
		t.Fatalf("identities should be swept: %v", ids)
	}
}

// TestDiscordBotCRUD mirrors the Slack DAL test for discord_bots.
func TestDiscordBotCRUD(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	uid, err := db.CreateUser(ctx, "alice", "h", true)
	if err != nil {
		t.Fatal(err)
	}
	agentID, _ := db.CreateAgent(ctx, Agent{UserID: uid, Name: "Coach2", Enabled: true})

	id, err := db.CreateDiscordBot(ctx, DiscordBot{
		UserID: uid, Label: "Server", TokenEnc: "enc-tok", AgentID: agentID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetDiscordBotIdentity(ctx, id, "999", "samwise"); err != nil {
		t.Fatal(err)
	}
	b, _ := db.GetDiscordBot(ctx, uid, id)
	if b.BotUserID != "999" || b.Username != "samwise" {
		t.Fatalf("identity cache: %+v", b)
	}
	if err := db.UpdateDiscordBotToken(ctx, uid, id, "enc-tok2"); err != nil {
		t.Fatal(err)
	}
	b, _ = db.GetDiscordBot(ctx, uid, id)
	if b.BotUserID != "" || b.TokenEnc != "enc-tok2" {
		t.Fatalf("token replace should clear identity: %+v", b)
	}
	if bb, err := db.DiscordBotAgentBinding(ctx, uid, agentID); err != nil || bb.ID != id {
		t.Fatalf("binding: %+v err=%v", bb, err)
	}
	if err := db.CreateIdentity(ctx, ChannelIdentity{UserID: uid, Channel: "discord", BotID: id, ExternalID: "U9", ChatID: "D9"}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteDiscordBot(ctx, uid, id); err != nil {
		t.Fatal(err)
	}
	if ids, _ := db.ListIdentitiesByUser(ctx, uid, "discord"); len(ids) != 0 {
		t.Fatalf("identities should be swept: %v", ids)
	}
}
