//go:build linux

package runtime

import (
	"os/exec"
	"syscall"
)

// applyIsolation makes cmd start as the given unprivileged uid/gid (with the
// supplementary groups). Requires the parent to be root; the kernel enforces the
// drop, so the agent's host tools run with that uid's filesystem access and
// nothing more. Called only when req.Isolation != nil.
func applyIsolation(cmd *exec.Cmd, iso *RunIsolation) error {
	groups := make([]uint32, 0, len(iso.Groups))
	for _, g := range iso.Groups {
		groups = append(groups, uint32(g))
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Credential = &syscall.Credential{
		Uid:    uint32(iso.UID),
		Gid:    uint32(iso.GID),
		Groups: groups,
	}
	return nil
}
