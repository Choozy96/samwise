package store

import (
	"context"
	"testing"
)

// TestTelegramUserIsPaired: a Telegram id counts as a registered user only when
// it has a DM identity (external_id = its own positive id) — being a member of a
// paired group (external_id = the negative chat id) does not make it paired. This
// is what gates write operations in group chats.
func TestTelegramUserIsPaired(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	uid, err := db.CreateUser(ctx, "alice", "h", false)
	if err != nil {
		t.Fatal(err)
	}

	// A DM pairing for telegram id 555.
	if err := db.CreateIdentity(ctx, ChannelIdentity{
		UserID: uid, Channel: "telegram", BotID: 0, ExternalID: "555", ChatID: "555",
	}); err != nil {
		t.Fatal(err)
	}
	// A paired group (negative chat id) owned by alice.
	if err := db.CreateIdentity(ctx, ChannelIdentity{
		UserID: uid, Channel: "telegram", BotID: 0, ExternalID: "-100200", ChatID: "-100200",
	}); err != nil {
		t.Fatal(err)
	}

	if ok, _ := db.TelegramUserIsPaired(ctx, 555); !ok {
		t.Error("a DM-paired telegram id should be reported as paired")
	}
	if ok, _ := db.TelegramUserIsPaired(ctx, 999); ok {
		t.Error("an unknown telegram id should not be paired")
	}
	// A positive sender id that only appears as a group member (never DM-paired)
	// is not a registered user.
	if ok, _ := db.TelegramUserIsPaired(ctx, 777); ok {
		t.Error("a group member who isn't DM-paired should not be paired")
	}
}
