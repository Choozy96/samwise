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

func newCredOrch(t *testing.T) (*Orchestrator, string, string, *runtime.RunIsolation) {
	t.Helper()
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
		cfg:       &config.Config{DBPath: filepath.Join(tmp, "data", "app.db"), AgentUIDBase: 20000},
		claudeDir: canonical,
	}
	iso := &runtime.RunIsolation{UID: os.Getuid(), GID: os.Getgid()}
	return o, credPath, filepath.Join(o.workspace(7), ".claude", ".credentials.json"), iso
}

// TestSetupRunClaudeDir verifies the credential is SYMLINKED to the shared
// canonical (so OAuth refresh stays coherent across runs), and that a refresh
// (claude replacing the symlink with a newer real file) is reconciled back to the
// canonical and the symlink restored.
func TestSetupRunClaudeDir(t *testing.T) {
	o, credPath, link, iso := newCredOrch(t)

	if err := o.setupRunClaudeDir(7, iso); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("credential should be a symlink to the shared canonical (coherent refresh)")
	}
	if got, _ := os.ReadFile(link); string(got) != `{"token":"v1"}` {
		t.Errorf("symlink resolves to wrong content: %s", got)
	}

	// Simulate claude refreshing: replace the symlink with a newer real file.
	_ = os.Remove(link)
	if err := os.WriteFile(link, []byte(`{"token":"v2-refreshed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	older := time.Now().Add(-time.Hour)
	_ = os.Chtimes(credPath, older, older)

	// The post-run reconcile (also run at next setup) pushes it back + relinks.
	o.reconcileClaudeCred(7)
	if got, _ := os.ReadFile(credPath); string(got) != `{"token":"v2-refreshed"}` {
		t.Errorf("refreshed token not synced to the canonical: %s", got)
	}
	if fi2, _ := os.Lstat(link); fi2.Mode()&os.ModeSymlink == 0 {
		t.Error("symlink should be restored after reconcile")
	}
}

// TestReconcileSkipsStaleOrInvalid guards two regressions: a NEWER but invalid
// token must not corrupt the canonical, and an OLDER real file (e.g. a leftover
// copy from the old build, or after a manual re-auth) must not clobber a fresher
// canonical.
func TestReconcileSkipsStaleOrInvalid(t *testing.T) {
	// Invalid (torn) but newer → not written back.
	o, credPath, link, iso := newCredOrch(t)
	if err := o.setupRunClaudeDir(7, iso); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(link)
	_ = os.WriteFile(link, []byte(`{"token":`), 0o600) // invalid JSON
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(credPath, old, old)
	o.reconcileClaudeCred(7)
	if got, _ := os.ReadFile(credPath); string(got) != `{"token":"v1"}` {
		t.Errorf("invalid token corrupted the canonical: %s", got)
	}

	// Older real file vs a freshly re-authed canonical → canonical wins.
	o2, cred2, link2, iso2 := newCredOrch(t)
	if err := o2.setupRunClaudeDir(7, iso2); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(link2)
	_ = os.WriteFile(link2, []byte(`{"token":"stale"}`), 0o600)
	stale := time.Now().Add(-time.Hour)
	_ = os.Chtimes(link2, stale, stale)                          // the copy is OLD
	_ = os.WriteFile(cred2, []byte(`{"token":"reauth"}`), 0o640) // canonical just re-authed (now)
	o2.reconcileClaudeCred(7)
	if got, _ := os.ReadFile(cred2); string(got) != `{"token":"reauth"}` {
		t.Errorf("stale copy clobbered a fresher canonical: %s", got)
	}
}
