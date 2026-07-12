//go:build linux

package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"samwise/internal/config"
	"samwise/internal/runtime"
)

// TestSetupRunClaudeDir verifies a run gets a private claude config dir with its
// own COPY of the shared credential (not a symlink), and that a token refresh in
// that copy is synced back to the canonical on the next run.
// (Uses the test process's own uid/gid so the chowns succeed without root.)
func TestSetupRunClaudeDir(t *testing.T) {
	tmp := t.TempDir()
	canonical := filepath.Join(tmp, "shared")
	if err := os.MkdirAll(canonical, 0o700); err != nil {
		t.Fatal(err)
	}
	credPath := filepath.Join(canonical, ".credentials.json")
	if err := os.WriteFile(credPath, []byte(`{"token":"v1"}`), 0o640); err != nil {
		t.Fatal(err)
	}

	o := &Orchestrator{
		cfg:       &config.Config{DBPath: filepath.Join(tmp, "data", "app.db")},
		claudeDir: canonical,
	}
	iso := &runtime.RunIsolation{UID: os.Getuid(), GID: os.Getgid()}

	// First run: the per-user dir gets a PRIVATE COPY (not a symlink to the shared file).
	if err := o.setupRunClaudeDir(7, iso); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(o.workspace(7), ".claude", ".credentials.json")
	fi, err := os.Lstat(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("credential should be a private copy, not a symlink to the shared file")
	}
	if got, _ := os.ReadFile(copyPath); string(got) != `{"token":"v1"}` {
		t.Errorf("copy has wrong content: %s", got)
	}

	// Simulate claude refreshing the token IN THIS RUN'S COPY (newer than canonical).
	if err := os.WriteFile(copyPath, []byte(`{"token":"v2-refreshed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	older := time.Now().Add(-time.Hour)
	_ = os.Chtimes(credPath, older, older) // make the canonical older than the refresh

	// Next run reconciles the newer copy back to the canonical.
	if err := o.setupRunClaudeDir(7, iso); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(credPath); string(got) != `{"token":"v2-refreshed"}` {
		t.Errorf("refreshed token not synced to the canonical: %s", got)
	}
	if got, _ := os.ReadFile(copyPath); string(got) != `{"token":"v2-refreshed"}` {
		t.Errorf("copy should hold the reconciled token: %s", got)
	}
	if fi2, _ := os.Lstat(copyPath); fi2.Mode()&os.ModeSymlink != 0 {
		t.Error("credential should remain a real copy, never a symlink")
	}
}

// TestSetupRunClaudeDirSkipsBadWriteback guards the corruption fix: if this run's
// copy isn't a complete, valid-JSON token (e.g. a torn write), reconcile must NOT
// overwrite the canonical — and the copy is then refreshed from the good
// canonical instead.
func TestSetupRunClaudeDirSkipsBadWriteback(t *testing.T) {
	tmp := t.TempDir()
	canonical := filepath.Join(tmp, "shared")
	if err := os.MkdirAll(canonical, 0o700); err != nil {
		t.Fatal(err)
	}
	credPath := filepath.Join(canonical, ".credentials.json")
	if err := os.WriteFile(credPath, []byte(`{"token":"good"}`), 0o640); err != nil {
		t.Fatal(err)
	}

	o := &Orchestrator{
		cfg:       &config.Config{DBPath: filepath.Join(tmp, "data", "app.db")},
		claudeDir: canonical,
	}
	iso := &runtime.RunIsolation{UID: os.Getuid(), GID: os.Getgid()}
	if err := o.setupRunClaudeDir(7, iso); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(o.workspace(7), ".claude", ".credentials.json")

	// Replace the copy with a truncated/invalid token, made newer than the canonical.
	if err := os.WriteFile(copyPath, []byte(`{"token":`), 0o600); err != nil {
		t.Fatal(err)
	}
	older := time.Now().Add(-time.Hour)
	_ = os.Chtimes(credPath, older, older)

	if err := o.setupRunClaudeDir(7, iso); err != nil {
		t.Fatal(err)
	}
	// Canonical untouched (invalid token never written back)...
	if got, _ := os.ReadFile(credPath); string(got) != `{"token":"good"}` {
		t.Errorf("canonical was corrupted by an invalid writeback: %s", got)
	}
	// ...and the copy is refreshed from the good canonical.
	if got, _ := os.ReadFile(copyPath); string(got) != `{"token":"good"}` {
		t.Errorf("copy should be refreshed from the good canonical: %s", got)
	}
}
