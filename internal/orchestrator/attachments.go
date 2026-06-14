package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaxAttachmentBytes is the hard per-file size cap for a saved attachment.
const MaxAttachmentBytes = 10 << 20 // 10 MiB

const maxInlineTextBytes = 64 << 10 // inline text files up to 64 KiB into the prompt

// inlineTextExts are file types whose content is inlined into the prompt (so they
// work even without the runtime's file tools).
var inlineTextExts = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".csv": true, ".tsv": true,
	".json": true, ".yaml": true, ".yml": true, ".xml": true, ".html": true,
	".htm": true, ".log": true, ".go": true, ".py": true, ".js": true, ".ts": true,
	".sh": true, ".sql": true, ".toml": true, ".ini": true, ".conf": true,
}

// SaveAttachment writes one attachment's bytes into the user's upload dir under a
// sanitized, unique name and returns it as an Attachment that the agent's tools
// can read. Small text files are inlined so they work even without those tools.
// Shared by the web upload handler and the Telegram bot.
func (o *Orchestrator) SaveAttachment(userID int64, name string, data []byte) (Attachment, error) {
	if len(data) > MaxAttachmentBytes {
		return Attachment{}, fmt.Errorf("%q is too large (max 10MB)", name)
	}
	dir := o.UploadDir(userID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Attachment{}, err
	}
	name = sanitizeAttachmentName(name)
	dest := filepath.Join(dir, fmt.Sprintf("%d-%s", time.Now().UnixNano(), name))
	if err := os.WriteFile(dest, data, 0o600); err != nil {
		return Attachment{}, err
	}
	abs, _ := filepath.Abs(dest)
	att := Attachment{Name: name, Path: abs}
	if inlineTextExts[strings.ToLower(filepath.Ext(name))] && len(data) <= maxInlineTextBytes {
		att.Text = string(data)
	}
	return att, nil
}

// attachmentKind categorizes a file by extension so the prompt can tell the agent
// how to handle it: view images/PDFs, but don't pretend to read video/audio.
func attachmentKind(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".heic", ".heif":
		return "image"
	case ".pdf":
		return "pdf"
	case ".mp4", ".mov", ".avi", ".mkv", ".webm", ".m4v", ".mpeg", ".mpg":
		return "video"
	case ".mp3", ".wav", ".ogg", ".oga", ".m4a", ".flac", ".aac", ".opus", ".amr":
		return "audio"
	}
	return "other"
}

// sanitizeAttachmentName strips any path components from a user-supplied filename.
func sanitizeAttachmentName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	if name == "" || name == "." || name == ".." {
		name = "file"
	}
	return name
}
