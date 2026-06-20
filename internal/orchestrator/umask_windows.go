//go:build windows

package orchestrator

// setRestrictiveUmask is a no-op on Windows (no umask; isolation is Linux-only).
func setRestrictiveUmask() {}
