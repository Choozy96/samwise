package mcpserver

import (
	"context"
	"strings"
	"testing"
)

// TestSendFileReadOnlyDenied: an unregistered (read-only) group sender can't
// trigger a file send.
func TestSendFileReadOnlyDenied(t *testing.T) {
	h, ctx := newJobHandlers(t)
	h.readOnly = true
	r, _, _ := h.sendFile(ctx, nil, sendFileIn{Path: "report.xlsx"})
	s := strings.ToLower(resultText(r))
	if !strings.Contains(s, "registered") && !strings.Contains(s, "can't") {
		t.Fatalf("read-only send_file should be denied, got: %q", s)
	}
}

// TestSendFileNoExecutor: with no sender wired, send_file fails cleanly rather
// than panicking.
func TestSendFileNoExecutor(t *testing.T) {
	SetFileSender(nil)
	h, ctx := newJobHandlers(t)
	r, _, _ := h.sendFile(ctx, nil, sendFileIn{Path: "report.xlsx"})
	if s := resultText(r); !strings.Contains(s, "isn't available") {
		t.Fatalf("expected unavailable message, got: %q", s)
	}
}

// TestSendFileWired: with a sender wired, send_file forwards the path + caption
// and reports success.
func TestSendFileWired(t *testing.T) {
	var gotPath, gotCaption string
	SetFileSender(func(_ context.Context, _, _, _ int64, p, c string) error {
		gotPath, gotCaption = p, c
		return nil
	})
	t.Cleanup(func() { SetFileSender(nil) })

	h, ctx := newJobHandlers(t)
	r, _, _ := h.sendFile(ctx, nil, sendFileIn{Path: "out/report.csv", Caption: "here"})
	if s := resultText(r); !strings.Contains(s, "Sent") {
		t.Fatalf("expected success, got: %q", s)
	}
	if gotPath != "out/report.csv" || gotCaption != "here" {
		t.Errorf("forwarded path=%q caption=%q", gotPath, gotCaption)
	}
}
