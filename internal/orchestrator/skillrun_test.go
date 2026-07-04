package orchestrator

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"samwise/internal/config"
	"samwise/internal/store"
)

func newSkillOrch(t *testing.T) (*Orchestrator, *store.DB, context.Context, int64) {
	t.Helper()
	tmp := t.TempDir()
	db, err := store.Open(filepath.Join(tmp, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	uid, _ := db.CreateUser(ctx, "alice", "h", true)
	o := &Orchestrator{cfg: &config.Config{DBPath: filepath.Join(tmp, "app.db")}, db: db,
		log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return o, db, ctx, uid
}

func TestRunSkillGating(t *testing.T) {
	o, db, ctx, uid := newSkillOrch(t)

	// Not runnable (no entrypoint).
	db.CreateSkill(ctx, store.Skill{UserID: uid, Name: "noentry", Content: "x", Enabled: true})
	if _, err := o.RunSkill(ctx, uid, "noentry", "in", false); err == nil || !strings.Contains(err.Error(), "no entrypoint") {
		t.Errorf("non-runnable skill should error, got %v", err)
	}

	// Disabled.
	db.CreateSkill(ctx, store.Skill{UserID: uid, Name: "off", Entrypoint: "main.py", Enabled: false})
	if _, err := o.RunSkill(ctx, uid, "off", "in", false); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("disabled skill should error, got %v", err)
	}

	// Read-only caller, paired-only skill → refused.
	db.CreateSkill(ctx, store.Skill{UserID: uid, Name: "priv", Entrypoint: "main.py", Enabled: true, Audience: "paired"})
	if _, err := o.RunSkill(ctx, uid, "priv", "in", true); err == nil || !strings.Contains(err.Error(), "registered users only") {
		t.Errorf("read-only caller on paired skill should be refused, got %v", err)
	}

	// Entrypoint escaping the bundle is rejected.
	db.CreateSkill(ctx, store.Skill{UserID: uid, Name: "escape", Entrypoint: "../../../etc/passwd", Enabled: true})
	if _, err := o.RunSkill(ctx, uid, "escape", "in", false); err == nil || !strings.Contains(err.Error(), "invalid entrypoint") {
		t.Errorf("path-escaping entrypoint should be rejected, got %v", err)
	}
}

// TestRunSkillExecutes runs a real Python entrypoint end-to-end (skipped where
// python3 isn't on PATH, e.g. some dev machines). It confirms input is passed as
// data and stdout is returned — and that an unregistered caller CAN run a skill
// opened to everyone.
func TestRunSkillExecutes(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	o, db, ctx, uid := newSkillOrch(t)

	bundle := o.SkillBundleDir(uid, "echo")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "import sys\nprint('got:', sys.argv[1] if len(sys.argv) > 1 else '')\n"
	if err := os.WriteFile(filepath.Join(bundle, "echo.py"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	db.CreateSkill(ctx, store.Skill{UserID: uid, Name: "echo", Entrypoint: "echo.py", Enabled: true, Audience: "everyone"})

	out, err := o.RunSkill(ctx, uid, "echo", "hello world", true) // read-only caller, everyone skill
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "got: hello world") {
		t.Errorf("unexpected output: %q", out)
	}
}
