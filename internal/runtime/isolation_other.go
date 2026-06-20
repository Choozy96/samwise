//go:build !linux

package runtime

import (
	"fmt"
	"os/exec"
)

// applyIsolation is unsupported off Linux (per-uid process credentials are a
// Linux feature). The orchestrator only sets req.Isolation after confirming it
// runs as root on Linux, so this should never be reached; erroring loudly beats
// silently running the agent unisolated.
func applyIsolation(_ *exec.Cmd, _ *RunIsolation) error {
	return fmt.Errorf("agent isolation is only supported on Linux")
}
