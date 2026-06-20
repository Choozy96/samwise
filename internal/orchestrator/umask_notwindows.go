//go:build !windows

package orchestrator

import "syscall"

// setRestrictiveUmask makes files this process creates owner-only by default
// (0600/0700), so DB sidecars, workspace files, and backups aren't born
// group/world-readable to a per-user agent uid.
func setRestrictiveUmask() { syscall.Umask(0o077) }
