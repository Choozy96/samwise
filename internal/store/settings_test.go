package store

import (
	"context"
	"testing"
)

// TestDistillNotifyDefaultAndUpdate verifies the distill-notify setting is OFF by
// default (distillation is silent; the note is opt-in) and round-trips.
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
	if s.DistillNotify {
		t.Errorf("distill_notify should default to false (silent)")
	}

	s.DistillNotify = true
	if err := db.UpdateSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	s2, _ := db.GetSettings(ctx, uid)
	if !s2.DistillNotify {
		t.Errorf("distill_notify should persist as true once opted in")
	}
}

// TestGroupReplyModeDefaultAndUpdate verifies group_reply_mode defaults to
// 'mention' (migration 0019) and round-trips.
func TestGroupReplyModeDefaultAndUpdate(t *testing.T) {
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
	if s.GroupReplyMode != "mention" {
		t.Errorf("group_reply_mode should default to 'mention', got %q", s.GroupReplyMode)
	}
	s.GroupReplyMode = "all"
	if err := db.UpdateSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	if s2, _ := db.GetSettings(ctx, uid); s2.GroupReplyMode != "all" {
		t.Errorf("group_reply_mode should persist as 'all', got %q", s2.GroupReplyMode)
	}
}

// TestExtraToolsDefaultAndUpdate verifies extra_tools defaults empty (migration
// 0021) and round-trips.
func TestExtraToolsDefaultAndUpdate(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	uid, err := db.CreateUser(ctx, "alice", "h", true)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := db.GetSettings(ctx, uid)
	if s.ExtraTools != "" {
		t.Errorf("extra_tools should default empty, got %q", s.ExtraTools)
	}
	s.ExtraTools = "WebFetch,WebSearch"
	if err := db.UpdateSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	if s2, _ := db.GetSettings(ctx, uid); s2.ExtraTools != "WebFetch,WebSearch" {
		t.Errorf("extra_tools should persist, got %q", s2.ExtraTools)
	}
}

// TestDistillTargetTZRoundTrip verifies the distillation notify target and
// pinned timezone default to blank (default channel / user's tz) and round-trip.
func TestDistillTargetTZRoundTrip(t *testing.T) {
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
	if s.DistillNotifyTarget != "" || s.DistillTZ != "" {
		t.Errorf("defaults should be blank: target=%q tz=%q", s.DistillNotifyTarget, s.DistillTZ)
	}
	s.DistillNotifyTarget = "tg:7:555"
	s.DistillTZ = "Asia/Singapore"
	if err := db.UpdateSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetSettings(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if got.DistillNotifyTarget != "tg:7:555" || got.DistillTZ != "Asia/Singapore" {
		t.Errorf("round-trip failed: target=%q tz=%q", got.DistillNotifyTarget, got.DistillTZ)
	}
}
