package orchestrator

import (
	"strings"
	"testing"
)

// TestVersionPromptLine: the version line is empty until set, then names the
// version so the agent can answer "what version are you?".
func TestVersionPromptLine(t *testing.T) {
	orig := appVersion
	t.Cleanup(func() { appVersion = orig })

	appVersion = ""
	if versionPromptLine() != "" {
		t.Errorf("unset version should produce no line, got %q", versionPromptLine())
	}
	SetVersion("v0.3.6")
	if got := versionPromptLine(); !strings.Contains(got, "v0.3.6") || !strings.Contains(got, "Samwise version") {
		t.Errorf("version line missing content: %q", got)
	}
}
