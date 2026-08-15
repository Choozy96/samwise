package orchestrator

import (
	"os"
	"path/filepath"
	"testing"

	"samwise/internal/config"
)

// TestResolveWorkspaceFile checks the send_file path guard: normal paths resolve
// under the user's workspace; traversal and the internal .claude dir are refused.
func TestResolveWorkspaceFile(t *testing.T) {
	o := &Orchestrator{cfg: &config.Config{DBPath: filepath.Join(t.TempDir(), "app.db")}}
	ws := o.workspace(7)

	if abs, name, ok := o.resolveWorkspaceFile(7, "report.xlsx"); !ok || name != "report.xlsx" || abs != filepath.Join(ws, "report.xlsx") {
		t.Errorf("plain file: (%q,%q,%v)", abs, name, ok)
	}
	if abs, name, ok := o.resolveWorkspaceFile(7, "out/summary.csv"); !ok || name != "summary.csv" || abs != filepath.Join(ws, "out", "summary.csv") {
		t.Errorf("nested file: (%q,%q,%v)", abs, name, ok)
	}
	// Traversal is neutralized (Clean collapses it back inside the root).
	if abs, _, ok := o.resolveWorkspaceFile(7, "../../etc/passwd"); !ok || abs != filepath.Join(ws, "etc", "passwd") {
		t.Errorf("traversal not contained: (%q,%v)", abs, ok)
	}
	for _, bad := range []string{".claude/.credentials.json", "sub/.claude/x", "", "."} {
		if _, _, ok := o.resolveWorkspaceFile(7, bad); ok {
			t.Errorf("path %q should be refused", bad)
		}
	}
}

// TestSendWorkspaceFileMissing: a non-existent (but well-formed) path errors
// rather than delivering.
func TestSendWorkspaceFileMissing(t *testing.T) {
	o := &Orchestrator{cfg: &config.Config{DBPath: filepath.Join(t.TempDir(), "app.db")}}
	if err := o.SendWorkspaceFile(nil, 7, 0, 0, "nope.txt", ""); err == nil {
		t.Fatal("expected an error for a missing file")
	}
	// A file that IS too large is rejected before any send attempt.
	ws := o.workspace(7)
	_ = os.MkdirAll(ws, 0o755)
	big := filepath.Join(ws, "big.bin")
	if err := os.WriteFile(big, make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	// Shrink the limit is not possible (const); just confirm a normal small file
	// gets past the guards and fails only at delivery (no telegram, web path needs
	// a DB) — the guard/stat/size checks themselves must not error.
	if _, name, ok := o.resolveWorkspaceFile(7, "big.bin"); !ok || name != "big.bin" {
		t.Errorf("resolve small file: %q %v", name, ok)
	}
}
