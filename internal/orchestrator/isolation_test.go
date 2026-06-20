package orchestrator

import (
	"testing"

	"samwise/internal/config"
)

// TestRunIsolation checks the per-user OS identity: uid and gid are base+userID,
// and the shared credentials gid is carried as a supplementary group (so the
// agent can read the claude auth dir but nothing maps two users to one uid).
func TestRunIsolation(t *testing.T) {
	o := &Orchestrator{cfg: &config.Config{AgentUIDBase: 20000, AgentCredGID: 10002}}

	a := o.runIsolation(2)
	if a.UID != 20002 || a.GID != 20002 {
		t.Errorf("user 2 should map to uid/gid 20002, got %d/%d", a.UID, a.GID)
	}
	if len(a.Groups) != 1 || a.Groups[0] != 10002 {
		t.Errorf("expected supplementary cred gid [10002], got %v", a.Groups)
	}

	// Distinct users get distinct uids (no collision => no cross-user access).
	b := o.runIsolation(3)
	if b.UID == a.UID {
		t.Errorf("different users must get different uids: %d == %d", a.UID, b.UID)
	}
}
