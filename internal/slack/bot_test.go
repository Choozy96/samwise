package slack

import "testing"

// TestExtractFiles parses attached-file refs from a raw Events API payload,
// preferring the download URL.
func TestExtractFiles(t *testing.T) {
	payload := []byte(`{"event":{"type":"message","files":[
		{"name":"report.pdf","url_private":"https://p/x","url_private_download":"https://d/x"},
		{"name":"","url_private":"https://p/y"},
		{"name":"skipme"}]}}`)
	got := extractFiles(payload)
	if len(got) != 2 {
		t.Fatalf("want 2 refs, got %+v", got)
	}
	if got[0].Name != "report.pdf" || got[0].URL != "https://d/x" {
		t.Errorf("ref0: %+v (download URL should win)", got[0])
	}
	if got[1].Name != "file" || got[1].URL != "https://p/y" {
		t.Errorf("ref1: %+v (missing name defaults)", got[1])
	}
	if extractFiles([]byte("junk")) != nil {
		t.Error("junk payload should yield nil")
	}
}

// TestMentionHelpers: detection + stripping.
func TestMentionHelpers(t *testing.T) {
	if !mentionsBot("hey <@U0BOT> hi", "U0BOT") {
		t.Error("mention not detected")
	}
	if mentionsBot("hey <@U0OTHER> hi", "U0BOT") || mentionsBot("plain", "U0BOT") || mentionsBot("x", "") {
		t.Error("false positive")
	}
	if got := stripMention("<@U0BOT> summarize this", "U0BOT"); got != "summarize this" {
		t.Errorf("strip: %q", got)
	}
	if got := stripMention("do it <@U0BOT>", "U0BOT"); got != "do it" {
		t.Errorf("strip trailing: %q", got)
	}
}

// TestReplyThreadTS: DMs unthreaded; channel replies thread under the message
// (or its existing thread).
func TestReplyThreadTS(t *testing.T) {
	if got := replyThreadTS(true, "1.1", "2.2"); got != "" {
		t.Errorf("DM should not thread: %q", got)
	}
	if got := replyThreadTS(false, "", "2.2"); got != "2.2" {
		t.Errorf("new thread under the message: %q", got)
	}
	if got := replyThreadTS(false, "1.1", "2.2"); got != "1.1" {
		t.Errorf("stay in the existing thread: %q", got)
	}
}

// TestChannelMessageDedup: mention-bearing channel messages are left to
// app_mention; un-mentioned ones only handled in "all" mode.
func TestChannelMessageDedup(t *testing.T) {
	if shouldHandleChannelMessage("<@U0BOT> hi", "U0BOT", "all") {
		t.Error("mention must be deduped even in all mode")
	}
	if shouldHandleChannelMessage("plain chatter", "U0BOT", "mention") {
		t.Error("mention mode must ignore un-addressed messages")
	}
	if !shouldHandleChannelMessage("plain chatter", "U0BOT", "all") {
		t.Error("all mode should handle un-addressed messages")
	}
}
