package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"samwise/internal/config"
)

func newAttachOrch(t *testing.T) *Orchestrator {
	t.Helper()
	return &Orchestrator{cfg: &config.Config{DBPath: filepath.Join(t.TempDir(), "app.db")}}
}

// TestSaveAttachmentInlinesText: a small text file is written to the user's
// upload dir and its content is inlined; the file exists on disk.
func TestSaveAttachmentInlinesText(t *testing.T) {
	o := newAttachOrch(t)
	att, err := o.SaveAttachment(1, "notes.md", []byte("hello world"))
	if err != nil {
		t.Fatal(err)
	}
	if att.Text != "hello world" {
		t.Errorf("text not inlined: %q", att.Text)
	}
	if !strings.HasPrefix(att.Path, o.UploadDir(1)) {
		t.Errorf("path %q not under upload dir %q", att.Path, o.UploadDir(1))
	}
	if _, err := os.Stat(att.Path); err != nil {
		t.Errorf("file not written: %v", err)
	}
}

// TestSaveAttachmentBinaryNotInlined: a non-text file is saved but not inlined.
func TestSaveAttachmentBinaryNotInlined(t *testing.T) {
	o := newAttachOrch(t)
	att, err := o.SaveAttachment(1, "pic.png", []byte{0x89, 0x50, 0x4e, 0x47})
	if err != nil {
		t.Fatal(err)
	}
	if att.Text != "" {
		t.Errorf("binary should not be inlined, got %q", att.Text)
	}
}

// TestSaveAttachmentSanitizesName: path components are stripped from the name.
func TestSaveAttachmentSanitizesName(t *testing.T) {
	o := newAttachOrch(t)
	att, err := o.SaveAttachment(1, "../../etc/evil.sh", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if att.Name != "evil.sh" {
		t.Errorf("name not sanitized: %q", att.Name)
	}
	if !strings.HasPrefix(att.Path, o.UploadDir(1)) {
		t.Errorf("escaped upload dir: %q", att.Path)
	}
}

// TestSaveAttachmentRejectsOversize: a file past the cap errors.
func TestSaveAttachmentRejectsOversize(t *testing.T) {
	o := newAttachOrch(t)
	if _, err := o.SaveAttachment(1, "big.bin", make([]byte, MaxAttachmentBytes+1)); err == nil {
		t.Error("expected oversize error, got nil")
	}
}
