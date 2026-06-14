package orchestrator

import (
	"strings"
	"testing"
)

func TestAttachmentKind(t *testing.T) {
	cases := map[string]string{
		"photo.jpg": "image", "x.PNG": "image", "a.heic": "image",
		"doc.pdf":  "pdf",
		"clip.mp4": "video", "v.mov": "video", "video-note.mp4": "video",
		"voice.ogg": "audio", "song.mp3": "audio", "a.opus": "audio",
		"notes.txt": "other", "data.bin": "other",
	}
	for name, want := range cases {
		if got := attachmentKind(name); got != want {
			t.Errorf("%s: got %q want %q", name, got, want)
		}
	}
}

// TestComposeWithAttachments checks type-aware prompting: images say "view",
// video/audio warn against guessing, text is inlined.
func TestComposeWithAttachments(t *testing.T) {
	atts := []Attachment{
		{Name: "shot.png", Path: "/w/shot.png"},
		{Name: "clip.mp4", Path: "/w/clip.mp4"},
		{Name: "notes.txt", Path: "/w/notes.txt", Text: "hello"},
	}
	stored, prompt := composeWithAttachments("look at these", atts)
	if !strings.Contains(stored, "shot.png") || !strings.Contains(stored, "clip.mp4") {
		t.Errorf("stored should list names: %q", stored)
	}
	if !strings.Contains(prompt, "View it with your Read tool") {
		t.Error("image should get a view instruction")
	}
	if !strings.Contains(prompt, "cannot watch video") {
		t.Error("video should warn against guessing")
	}
	if !strings.Contains(prompt, "hello") {
		t.Error("text should be inlined")
	}
}
