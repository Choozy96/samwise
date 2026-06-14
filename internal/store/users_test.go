package store

import (
	"context"
	"errors"
	"testing"

	"samwise/internal/auth"
)

// TestUpdatePassword verifies a password change persists a new verifiable hash,
// invalidates the old password, and reports ErrNotFound for an unknown user.
func TestUpdatePassword(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	oldHash, err := auth.HashPassword("originalpw")
	if err != nil {
		t.Fatal(err)
	}
	id, err := db.CreateUser(ctx, "alice", oldHash, true)
	if err != nil {
		t.Fatal(err)
	}

	newHash, err := auth.HashPassword("brandnewpw")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdatePassword(ctx, id, newHash); err != nil {
		t.Fatalf("update: %v", err)
	}

	u, err := db.GetUserByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.VerifyPassword("brandnewpw", u.PasswordHash); err != nil {
		t.Errorf("new password should verify: %v", err)
	}
	if err := auth.VerifyPassword("originalpw", u.PasswordHash); !errors.Is(err, auth.ErrMismatch) {
		t.Errorf("old password should no longer verify, got %v", err)
	}

	if err := db.UpdatePassword(ctx, 99999, newHash); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for unknown user, got %v", err)
	}
}
