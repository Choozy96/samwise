package discord

import (
	"strings"
	"testing"
)

// TestMentionHelpers: detection + stripping incl. the nickname form <@!id>.
func TestMentionHelpers(t *testing.T) {
	if !mentionsBot("hey <@123> hi", "123") || !mentionsBot("hey <@!123> hi", "123") {
		t.Error("mention not detected")
	}
	if mentionsBot("hey <@456> hi", "123") || mentionsBot("plain", "123") || mentionsBot("x", "") {
		t.Error("false positive")
	}
	if got := stripMention("<@123> summarize this", "123"); got != "summarize this" {
		t.Errorf("strip: %q", got)
	}
	if got := stripMention("<@!123> do it", "123"); got != "do it" {
		t.Errorf("strip nickname form: %q", got)
	}
}

// TestChunk: under-limit passes through; long text splits on newlines and
// reassembles exactly.
func TestChunk(t *testing.T) {
	if got := chunk("short", 100); len(got) != 1 || got[0] != "short" {
		t.Fatalf("short: %v", got)
	}
	long := strings.Repeat("line of text\n", 40)
	parts := chunk(long, 100)
	if len(parts) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(parts))
	}
	if strings.Join(parts, "") != long {
		t.Error("chunks must reassemble to the original")
	}
}
