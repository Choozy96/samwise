package store

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// applyMigrationsThrough opens a DB and applies embedded migrations whose version
// sorts <= maxVersion, so a test can build a pre-migration state and then apply a
// later migration in isolation.
func applyMigrationsThrough(t *testing.T, maxVersion string) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")
		if version > maxVersion {
			continue
		}
		b, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(b)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	return db
}

// applyMigration runs a single migration file by version.
func applyMigration(t *testing.T, db *DB, version string) {
	t.Helper()
	b, err := migrationsFS.ReadFile("migrations/" + version + ".sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(b)); err != nil {
		t.Fatalf("apply %s: %v", version, err)
	}
}

// TestMigrate0012MergesSplitThreads builds the pre-0012 world — a user+agent with
// SEPARATE web and telegram conversations, each holding messages — then applies
// 0012 and asserts the two threads merge into one canonical conversation with all
// messages preserved and tagged by their original channel.
func TestMigrate0012MergesSplitThreads(t *testing.T) {
	ctx := context.Background()
	db := applyMigrationsThrough(t, "0011_onboarding")

	// Minimal pre-migration rows (raw inserts; channel column doesn't exist yet).
	if _, err := db.Exec(`INSERT INTO users(id, username, password_hash, is_admin) VALUES(1,'alice','h',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO agents(id, user_id, name, is_default, enabled) VALUES(7,1,'Assistant',1,1)`); err != nil {
		t.Fatal(err)
	}
	// Two channel-split conversations for the same (user, agent).
	if _, err := db.Exec(`INSERT INTO conversations(id, user_id, channel, agent_id) VALUES(10,1,'web',7),(20,1,'telegram',7)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO messages(conversation_id, user_id, role, content) VALUES
		(10,1,'user','web one'),(10,1,'assistant','web reply'),(20,1,'user','telegram one')`); err != nil {
		t.Fatal(err)
	}

	applyMigration(t, db, "0012_unify_conversations")

	// Exactly one conversation should remain for (user, agent) — the lower id (10).
	var nConv, canon int64
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(MIN(id),0) FROM conversations WHERE user_id=1 AND agent_id=7`).
		Scan(&nConv, &canon); err != nil {
		t.Fatal(err)
	}
	if nConv != 1 || canon != 10 {
		t.Fatalf("expected 1 merged conversation id=10, got count=%d canon=%d", nConv, canon)
	}

	// All three messages now hang off the canonical thread, with channels tagged.
	msgs, err := db.RecentMessages(ctx, canon, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages on the merged thread, got %d", len(msgs))
	}
	gotWeb, gotTelegram := 0, 0
	for _, m := range msgs {
		switch m.Channel {
		case "web":
			gotWeb++
		case "telegram":
			gotTelegram++
		default:
			t.Errorf("message %d has unexpected channel %q", m.ID, m.Channel)
		}
	}
	if gotWeb != 2 || gotTelegram != 1 {
		t.Errorf("channel backfill wrong: web=%d telegram=%d (want 2/1)", gotWeb, gotTelegram)
	}
}

// TestMigrate0016PreservesIdentities builds the pre-0016 world (a paired Telegram
// identity and a pending pairing code), applies 0016, and asserts the
// channel_identities rebuild preserved the row as the legacy bot (bot_id=0) and
// that pairing_codes gained bot_id without losing data.
func TestMigrate0016PreservesIdentities(t *testing.T) {
	ctx := context.Background()
	db := applyMigrationsThrough(t, "0015_tg_format")

	if _, err := db.Exec(`INSERT INTO users(id, username, password_hash, is_admin) VALUES(1,'alice','h',1)`); err != nil {
		t.Fatal(err)
	}
	// Pre-0016 schema: channel_identities has no bot_id column.
	if _, err := db.Exec(`INSERT INTO channel_identities(user_id, channel, external_id, chat_id)
		VALUES(1,'telegram','42','4242')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO pairing_codes(code, channel, external_id, chat_id, expires_at)
		VALUES('ZZZ999','telegram','99','9999','2999-01-01 00:00:00')`); err != nil {
		t.Fatal(err)
	}

	applyMigration(t, db, "0016_telegram_bots")

	// The existing identity survived as the legacy bot (bot_id 0).
	ident, err := db.GetIdentityByExternal(ctx, "telegram", 0, "42")
	if err != nil {
		t.Fatalf("identity lost across 0016: %v", err)
	}
	if ident.UserID != 1 || ident.ChatID != "4242" || ident.BotID != 0 {
		t.Fatalf("identity mangled across rebuild: %+v", ident)
	}

	// The pending pairing code survived and defaulted to bot_id 0.
	pc, err := db.ConsumePairingCode(ctx, "ZZZ999")
	if err != nil {
		t.Fatalf("pairing code lost across 0016: %v", err)
	}
	if pc.BotID != 0 || pc.ExternalID != "99" {
		t.Fatalf("pairing code mangled across rebuild: %+v", pc)
	}
}
