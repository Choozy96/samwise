package orchestrator

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"samwise/internal/auth"
	"samwise/internal/store"
)

func TestCmdPassword(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("oldpass12")
	uid, err := db.CreateUser(ctx, "alice", hash, true)
	if err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{db: db}

	// Wrong current password is rejected; nothing changes.
	if got := o.cmdPassword(ctx, uid, "nope newpass12"); !strings.Contains(got, "incorrect") {
		t.Errorf("wrong current: %q", got)
	}
	// Too-short new password rejected.
	if got := o.cmdPassword(ctx, uid, "oldpass12 short"); !strings.Contains(got, "at least 8") {
		t.Errorf("short new: %q", got)
	}
	// Bad arg count.
	if got := o.cmdPassword(ctx, uid, "onlyone"); !strings.Contains(got, "Usage") {
		t.Errorf("usage: %q", got)
	}
	// Success — the new password verifies, the old one no longer does.
	if got := o.cmdPassword(ctx, uid, "oldpass12 newpass12"); !strings.Contains(got, "changed") {
		t.Errorf("change: %q", got)
	}
	u, _ := db.GetUserByID(ctx, uid)
	if err := auth.VerifyPassword("newpass12", u.PasswordHash); err != nil {
		t.Errorf("new password should verify: %v", err)
	}
	if err := auth.VerifyPassword("oldpass12", u.PasswordHash); err == nil {
		t.Error("old password should no longer verify")
	}
}

func TestCmdFormat(t *testing.T) {
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
	o := &Orchestrator{db: db}

	// Default shows markdown.
	if got := o.cmdFormat(ctx, uid, ""); !strings.Contains(got, "markdown") {
		t.Errorf("default status: %q", got)
	}
	// Set to html.
	o.cmdFormat(ctx, uid, "html")
	if s, _ := db.GetSettings(ctx, uid); s.TgFormat != "html" {
		t.Errorf("didn't set html: %q", s.TgFormat)
	}
	// "rich"/"telegram" are aliases for markdown now.
	o.cmdFormat(ctx, uid, "rich")
	if s, _ := db.GetSettings(ctx, uid); s.TgFormat != "markdown" {
		t.Errorf("rich alias should map to markdown: %q", s.TgFormat)
	}
	// Plain disables formatting.
	o.cmdFormat(ctx, uid, "plain")
	if s, _ := db.GetSettings(ctx, uid); s.TgFormat != "plain" {
		t.Errorf("didn't set plain: %q", s.TgFormat)
	}
	// Invalid value is rejected, leaves the setting unchanged.
	if got := o.cmdFormat(ctx, uid, "xyz"); !strings.Contains(got, "Unknown") {
		t.Errorf("invalid should be rejected: %q", got)
	}
	if s, _ := db.GetSettings(ctx, uid); s.TgFormat != "plain" {
		t.Errorf("invalid input changed the setting: %q", s.TgFormat)
	}
}

func TestCmdGroupReply(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	uid, _ := db.CreateUser(ctx, "alice", "h", true)
	o := &Orchestrator{db: db}

	// Default shows mention.
	if got := o.cmdGroupReply(ctx, uid, ""); !strings.Contains(got, "mention") {
		t.Errorf("default status: %q", got)
	}
	// Set to all, then back to mention.
	o.cmdGroupReply(ctx, uid, "all")
	if s, _ := db.GetSettings(ctx, uid); s.GroupReplyMode != "all" {
		t.Errorf("didn't set all: %q", s.GroupReplyMode)
	}
	o.cmdGroupReply(ctx, uid, "mention")
	if s, _ := db.GetSettings(ctx, uid); s.GroupReplyMode != "mention" {
		t.Errorf("didn't set mention: %q", s.GroupReplyMode)
	}
	// Invalid leaves it unchanged.
	if got := o.cmdGroupReply(ctx, uid, "xyz"); !strings.Contains(got, "Unknown") {
		t.Errorf("invalid should be rejected: %q", got)
	}
	if s, _ := db.GetSettings(ctx, uid); s.GroupReplyMode != "mention" {
		t.Errorf("invalid changed the setting: %q", s.GroupReplyMode)
	}
}

func TestCmdTimezoneAndDelivery(t *testing.T) {
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
	o := &Orchestrator{db: db}

	// Timezone: invalid rejected, valid saved.
	if got := o.cmdTimezone(ctx, uid, "Not/AZone"); !strings.Contains(got, "Unknown timezone") {
		t.Errorf("bad tz should be rejected: %q", got)
	}
	o.cmdTimezone(ctx, uid, "Asia/Singapore")
	if s, _ := db.GetSettings(ctx, uid); s.Timezone != "Asia/Singapore" {
		t.Errorf("tz not saved: %q", s.Timezone)
	}

	// Delivery: bad value rejected, telegram saved.
	if got := o.cmdDelivery(ctx, uid, "carrier-pigeon"); !strings.Contains(got, "/delivery web") {
		t.Errorf("bad delivery should be rejected: %q", got)
	}
	o.cmdDelivery(ctx, uid, "telegram")
	if s, _ := db.GetSettings(ctx, uid); s.DeliveryChannel != "telegram" {
		t.Errorf("delivery not saved: %q", s.DeliveryChannel)
	}
}

func TestCmdAdmin(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	adminHash, _ := auth.HashPassword("adminpass1")
	adminID, _ := db.CreateUser(ctx, "boss", adminHash, true)
	plebHash, _ := auth.HashPassword("plebpass12")
	plebID, _ := db.CreateUser(ctx, "pleb", plebHash, false)
	o := &Orchestrator{db: db}

	// Non-admins are refused outright.
	if got := o.cmdAdmin(ctx, plebID, "users"); !strings.Contains(got, "admins only") {
		t.Errorf("non-admin should be refused: %q", got)
	}
	// Admin can list (sees both, admin tagged).
	if got := o.cmdAdmin(ctx, adminID, "users"); !strings.Contains(got, "boss") || !strings.Contains(got, "pleb") || !strings.Contains(got, "[admin]") {
		t.Errorf("list: %q", got)
	}
	// Create a user; it persists and the password verifies.
	if got := o.cmdAdmin(ctx, adminID, "add alice newpass12"); !strings.Contains(got, "Created") {
		t.Errorf("add: %q", got)
	}
	a, _ := db.GetUserByUsername(ctx, "alice")
	if a == nil || a.IsAdmin || auth.VerifyPassword("newpass12", a.PasswordHash) != nil {
		t.Errorf("created user wrong: %+v", a)
	}
	// Duplicate + short-password are rejected.
	if got := o.cmdAdmin(ctx, adminID, "add alice another12"); !strings.Contains(got, "taken") {
		t.Errorf("dup: %q", got)
	}
	if got := o.cmdAdmin(ctx, adminID, "add bob short"); !strings.Contains(got, "at least 8") {
		t.Errorf("short pw: %q", got)
	}
	// Disable / enable.
	o.cmdAdmin(ctx, adminID, "disable alice")
	if a, _ = db.GetUserByUsername(ctx, "alice"); !a.Disabled {
		t.Error("alice should be disabled")
	}
	o.cmdAdmin(ctx, adminID, "enable alice")
	if a, _ = db.GetUserByUsername(ctx, "alice"); a.Disabled {
		t.Error("alice should be enabled")
	}
	// Admins can't be disabled or password-reset via these commands.
	if got := o.cmdAdmin(ctx, adminID, "disable boss"); !strings.Contains(got, "can't be disabled") {
		t.Errorf("disable admin: %q", got)
	}
	if got := o.cmdAdmin(ctx, adminID, "resetpw boss whatever12"); !strings.Contains(got, "Settings") {
		t.Errorf("reset admin: %q", got)
	}
	// Reset a normal user's password.
	if got := o.cmdAdmin(ctx, adminID, "resetpw alice resetpass12"); !strings.Contains(got, "Reset") {
		t.Errorf("resetpw: %q", got)
	}
	if a, _ = db.GetUserByUsername(ctx, "alice"); auth.VerifyPassword("resetpass12", a.PasswordHash) != nil {
		t.Error("reset password should verify")
	}
	// Unknown user.
	if got := o.cmdAdmin(ctx, adminID, "disable ghost"); !strings.Contains(got, "No user") {
		t.Errorf("unknown user: %q", got)
	}
}

func TestCmdBindAndBots(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	uid, _ := db.CreateUser(ctx, "alice", "h", true)
	work, _ := db.CreateAgent(ctx, store.Agent{UserID: uid, Name: "work", Enabled: true})
	personal, _ := db.CreateAgent(ctx, store.Agent{UserID: uid, Name: "personal", Enabled: true})
	botID, _ := db.CreateTelegramBot(ctx, store.TelegramBot{UserID: uid, Label: "Bot", TokenEnc: "e", AgentID: work, Enabled: true})
	o := &Orchestrator{db: db}

	idStr := strconv.FormatInt(botID, 10)

	// Rebind work -> personal.
	if got := o.cmdBind(ctx, uid, idStr+" personal"); !strings.Contains(got, "personal") {
		t.Errorf("bind reply: %q", got)
	}
	if b, _ := db.GetTelegramBot(ctx, uid, botID); b.AgentID != personal {
		t.Errorf("bind didn't apply: agent_id=%d want %d", b.AgentID, personal)
	}
	// Unbind.
	o.cmdBind(ctx, uid, idStr+" none")
	if b, _ := db.GetTelegramBot(ctx, uid, botID); b.AgentID != 0 {
		t.Errorf("unbind didn't apply: agent_id=%d", b.AgentID)
	}
	// Unknown bot / agent are rejected.
	if got := o.cmdBind(ctx, uid, "999 personal"); !strings.Contains(got, "No bot") {
		t.Errorf("unknown bot: %q", got)
	}
	if got := o.cmdBind(ctx, uid, idStr+" nope"); !strings.Contains(got, "No agent") {
		t.Errorf("unknown agent: %q", got)
	}
	// /bots lists the bot.
	if got := o.cmdBots(ctx, uid); !strings.Contains(got, "Bot") || !strings.Contains(got, idStr) {
		t.Errorf("bots list: %q", got)
	}
}

func TestCmdRemind(t *testing.T) {
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
	o := &Orchestrator{db: db}
	o.cmdTimezone(ctx, uid, "Asia/Singapore")

	// Missing message is rejected; no job created.
	if got := o.cmdRemind(ctx, uid, "08:00"); !strings.Contains(got, "Usage") {
		t.Errorf("time-only should ask for usage: %q", got)
	}
	// A daily reminder creates an enabled direct_message job.
	if got := o.cmdRemind(ctx, uid, "daily 08:30 take meds"); !strings.Contains(got, "Reminder") {
		t.Errorf("daily remind failed: %q", got)
	}
	jobs, err := db.ListJobs(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, j := range jobs {
		if j.Type == "direct_message" && j.ScheduleSpec == "daily@08:30" && j.Enabled {
			found = true
			if !strings.Contains(j.Payload, "take meds") {
				t.Errorf("payload missing message: %q", j.Payload)
			}
		}
	}
	if !found {
		t.Errorf("daily reminder job not created: %+v", jobs)
	}

	// /reminders lists it back.
	if got := o.cmdReminders(ctx, uid); !strings.Contains(got, "take meds") {
		t.Errorf("reminders should list it: %q", got)
	}
}
