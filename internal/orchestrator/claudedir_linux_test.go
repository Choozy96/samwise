//go:build linux

package orchestrator

import (
	"os"
	"path/filepath"
	"testing"

	"samwise/internal/config"
	"samwise/internal/runtime"
)

// TestSetupRunClaudeDir verifies a run gets a private claude config dir with the
// shared credential symlinked in, and that a token refresh which replaced the
// symlink with a real file is synced back to the shared credential and relinked.
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

	// First run: the per-user dir holds a symlink to the shared credential.
	if err := o.setupRunClaudeDir(7, iso); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(o.workspace(7), ".claude", ".credentials.json")
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("credential should be a symlink to the shared file")
	}
	if got, _ := os.ReadFile(link); string(got) != `{"token":"v1"}` {
		t.Errorf("symlink resolves to wrong content: %s", got)
	}

	// Simulate a refresh that replaced the symlink with a real file.
	_ = os.Remove(link)
	if err := os.WriteFile(link, []byte(`{"token":"v2-refreshed"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Next run reconciles: new token written back to the shared file, symlink restored.
	if err := o.setupRunClaudeDir(7, iso); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(credPath); string(got) != `{"token":"v2-refreshed"}` {
		t.Errorf("refreshed token not synced to the shared credential: %s", got)
	}
	if fi2, _ := os.Lstat(link); fi2.Mode()&os.ModeSymlink == 0 {
		t.Error("symlink should be restored after reconcile")
	}
}

// TestSetupRunClaudeDirSkipsBadWriteback guards the corruption fix: if the file
// that replaced the symlink isn't a complete, valid-JSON token (e.g. a partial
// write from a concurrent run), reconcile must NOT overwrite the shared
// credential — that file is auth for every user.
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
	link := filepath.Join(o.workspace(7), ".claude", ".credentials.json")

	// Replace the symlink with a truncated / invalid file (a torn concurrent write).
	_ = os.Remove(link)
	if err := os.WriteFile(link, []byte(`{"token":`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := o.setupRunClaudeDir(7, iso); err != nil {
		t.Fatal(err)
	}
	// The shared credential must be untouched, and the symlink restored.
	if got, _ := os.ReadFile(credPath); string(got) != `{"token":"good"}` {
		t.Errorf("shared credential was corrupted by an invalid writeback: %s", got)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Error("symlink should be restored even when writeback is skipped")
	}
}
