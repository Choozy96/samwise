package orchestrator

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"samwise/internal/config"
	"samwise/internal/secretbox"
	"samwise/internal/store"
)

// TestUserSecretsEnvRoundTrip stores encrypted secrets and confirms the
// orchestrator decrypts them back into an env map for injection.
func TestUserSecretsEnvRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	uid, err := db.CreateUser(ctx, "alice", "h", true)
	if err != nil {
		t.Fatal(err)
	}

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	box, err := secretbox.New(key)
	if err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{cfg: &config.Config{}, db: db, log: slog.New(slog.NewTextHandler(io.Discard, nil)), box: box}

	enc, err := box.Encrypt([]byte("tok-secret-123"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetSecret(ctx, uid, "TODOIST_TOKEN", enc, "value"); err != nil {
		t.Fatal(err)
	}

	env := o.userSecretsEnv(ctx, uid)
	if env["TODOIST_TOKEN"] != "tok-secret-123" {
		t.Errorf("decrypted env wrong: %q", env["TODOIST_TOKEN"])
	}

	// A user with no secrets gets nil (no env injected).
	other, _ := db.CreateUser(ctx, "bob", "h", false)
	if env := o.userSecretsEnv(ctx, other); env != nil {
		t.Errorf("expected nil env for user with no secrets, got %v", env)
	}
}
