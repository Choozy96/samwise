package store

import (
	"context"
	"testing"
)

// TestDistillNotifyDefaultAndUpdate verifies the distill-notify setting defaults
// to on (migration 0017) and round-trips through UpdateSettings.
func TestDistillNotifyDefaultAndUpdate(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	uid, err := db.CreateUser(ctx, "alice", "h", true)
	if err != nil {
		t.Fatal(err)
	}

	s, err := db.GetSettings(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if !s.DistillNotify {
		t.Errorf("distill_notify should default to true")
	}

	s.DistillNotify = false
	if err := db.UpdateSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	s2, _ := db.GetSettings(ctx, uid)
	if s2.DistillNotify {
		t.Errorf("distill_notify should persist as false")
	}
}
