package store

import (
	"context"
	"testing"
)

// TestSecretsCRUD covers upsert-by-name, listing, and delete, scoped per user.
func TestSecretsCRUD(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	uid, err := db.CreateUser(ctx, "alice", "h", true)
	if err != nil {
		t.Fatal(err)
	}

	if err := db.SetSecret(ctx, uid, "TODOIST_TOKEN", "enc-v1", "value"); err != nil {
		t.Fatal(err)
	}
	// Upsert by name replaces the value, not adds a row.
	if err := db.SetSecret(ctx, uid, "TODOIST_TOKEN", "enc-v2", "value"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSecret(ctx, uid, "NOTION_TOKEN", "enc-n", "oauth"); err != nil {
		t.Fatal(err)
	}

	secs, err := db.ListSecrets(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(secs) != 2 {
		t.Fatalf("expected 2 secrets, got %d", len(secs))
	}
	// Ordered by name: NOTION_TOKEN then TODOIST_TOKEN.
	if secs[0].Name != "NOTION_TOKEN" || secs[1].Name != "TODOIST_TOKEN" {
		t.Errorf("unexpected order/names: %+v", secs)
	}
	if secs[1].ValueEnc != "enc-v2" {
		t.Errorf("upsert didn't replace value: %q", secs[1].ValueEnc)
	}
	if secs[0].Kind != "oauth" || secs[1].Kind != "value" {
		t.Errorf("kind not persisted: %q, %q", secs[0].Kind, secs[1].Kind)
	}

	// Cross-user isolation.
	other, _ := db.CreateUser(ctx, "bob", "h", false)
	if os, _ := db.ListSecrets(ctx, other); len(os) != 0 {
		t.Errorf("bob should see no secrets, got %d", len(os))
	}

	if err := db.DeleteSecret(ctx, uid, "TODOIST_TOKEN"); err != nil {
		t.Fatal(err)
	}
	secs, _ = db.ListSecrets(ctx, uid)
	if len(secs) != 1 || secs[0].Name != "NOTION_TOKEN" {
		t.Errorf("delete wrong: %+v", secs)
	}
}
