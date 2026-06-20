//go:build linux

package runtime

import (
	"os/exec"
	"testing"
)

// TestApplyIsolation checks that the per-run uid/gid and supplementary groups
// are wired into the child's process credential (Linux-only).
func TestApplyIsolation(t *testing.T) {
	cmd := exec.Command("true")
	if err := applyIsolation(cmd, &RunIsolation{UID: 20005, GID: 20005, Groups: []int{10002}}); err != nil {
		t.Fatal(err)
	}
	cred := cmd.SysProcAttr.Credential
	if cred == nil {
		t.Fatal("expected a process credential to be set")
	}
	if cred.Uid != 20005 || cred.Gid != 20005 {
		t.Errorf("uid/gid = %d/%d, want 20005/20005", cred.Uid, cred.Gid)
	}
	if len(cred.Groups) != 1 || cred.Groups[0] != 10002 {
		t.Errorf("supplementary groups = %v, want [10002]", cred.Groups)
	}
}
