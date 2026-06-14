package web

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeBundlePath(t *testing.T) {
	dir := filepath.Join("data", "workspaces", "1", ".claude", "skills", "todoist")

	// Legitimate paths resolve under dir.
	for _, rel := range []string{"SKILL.md", "scripts/get_tasks.py", "assets/x.json", "./scripts/a.py"} {
		got, ok := safeBundlePath(dir, rel)
		if !ok || !strings.HasPrefix(got, dir) {
			t.Errorf("%q: got %q ok=%v, want under %q", rel, got, ok, dir)
		}
	}

	// Traversal attempts are neutralized (collapsed under dir) or rejected — never
	// resolve outside the bundle dir.
	for _, rel := range []string{"../../../etc/passwd", "../todoist-secrets", "scripts/../../../x", "..\\..\\win"} {
		got, ok := safeBundlePath(dir, rel)
		if ok && !strings.HasPrefix(got, dir) {
			t.Errorf("%q escaped to %q", rel, got)
		}
	}
}
