package web

import (
	"io"
	"path/filepath"
	"testing"
)

// TestHiddenWorkspacePath guards the Files browser's sensitive-dir rule: no
// path may reach into .claude (credential symlink + transcripts) or cache dirs,
// in any position or with traversal tricks.
func TestHiddenWorkspacePath(t *testing.T) {
	cases := map[string]bool{
		"uploads/report.pdf":            false,
		"skills/bus/main.py":            false,
		".claude/.credentials.json":     true,
		".claude/projects/x.jsonl":      true,
		"skills/bus/.venv/bin/python":   true,
		"a/__pycache__/mod.pyc":         true,
		"sub/.git/config":               true,
		"notes/.claude-notes.md":        false, // only exact segment matches hide
		"skills/../.claude/credentials": true,  // traversal spelled out still names .claude
	}
	for rel, want := range cases {
		if got := hiddenWorkspacePath(rel); got != want {
			t.Errorf("hiddenWorkspacePath(%q) = %v, want %v", rel, got, want)
		}
	}
}

// TestWorkspacePath covers the combined resolver used by every read AND write
// op: traversal out of the root and hidden segments are rejected; normal paths
// (and the root itself) resolve inside the workspace.
func TestWorkspacePath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ws")

	if got, ok := workspacePath(root, ""); !ok || got != root {
		t.Errorf("root should resolve to itself: (%q,%v)", got, ok)
	}
	if got, ok := workspacePath(root, "uploads/a.txt"); !ok || got != filepath.Join(root, "uploads", "a.txt") {
		t.Errorf("normal path: (%q,%v)", got, ok)
	}
	// Traversal collapses back inside the root (never escapes).
	if got, ok := workspacePath(root, "../../etc/passwd"); !ok || got != filepath.Join(root, "etc", "passwd") {
		t.Errorf("traversal should be neutralized inside the root: (%q,%v)", got, ok)
	}
	if _, ok := workspacePath(root, ".claude/.credentials.json"); ok {
		t.Error("hidden dir must be rejected")
	}
	if _, ok := workspacePath(root, "skills/x/.venv/pyvenv.cfg"); ok {
		t.Error("nested hidden dir must be rejected")
	}
}

// TestValidEntryName covers new-name validation for upload/mkdir/rename: one
// plain segment, no traversal, no hidden names.
func TestValidEntryName(t *testing.T) {
	cases := map[string]bool{
		"report.pdf": true,
		"my-folder":  true,
		".env":       true, // dotfiles are fine — only the hidden-dir names are blocked
		"":           false,
		".":          false,
		"..":         false,
		"a/b":        false,
		`a\b`:        false,
		".claude":    false,
		".venv":      false,
		".git":       false,
	}
	for name, want := range cases {
		if got := validEntryName(name); got != want {
			t.Errorf("validEntryName(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestFilesURL checks flash/query assembly for redirect targets.
func TestFilesURL(t *testing.T) {
	if got := filesURL("", ""); got != "/files" {
		t.Errorf("root: %q", got)
	}
	if got := filesURL("a/b", "deleted"); got != "/files?msg=deleted&path=a%2Fb" {
		t.Errorf("dir+msg: %q", got)
	}
}

// TestFilesTemplateRenders is a render smoke test for the browser + viewer,
// exercising folders, breadcrumbs, and the editable form.
func TestFilesTemplateRenders(t *testing.T) {
	list := pageData{
		"Title": "Files", "User": nil, "Rel": "skills/bus",
		"Crumbs":    []crumb{{Name: "skills", Rel: "skills"}, {Name: "bus", Rel: "skills/bus"}},
		"ParentRel": "skills",
		"Entries": []wsEntry{
			{Name: "data", Rel: "skills/bus/data", Dir: true, Size: "—", Modified: "2026-06-24 10:00"},
			{Name: "main.py", Rel: "skills/bus/main.py", Size: "1.2 KB", Modified: "2026-06-24 10:00"},
		},
	}
	if err := tmpl.ExecuteTemplate(io.Discard, "files", list); err != nil {
		t.Fatalf("files template render: %v", err)
	}
	view := pageData{
		"Title": "Workspace file", "User": nil, "Path": "skills/bus/main.py",
		"DirRel": "skills/bus", "Content": "print('hi')", "Editable": true,
	}
	if err := tmpl.ExecuteTemplate(io.Discard, "wsfile", view); err != nil {
		t.Fatalf("wsfile template render: %v", err)
	}
}
