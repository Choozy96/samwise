package slack

import (
	"strings"
	"testing"
)

// TestMarkdownToMrkdwn covers the conversion table: bold, strike, links,
// headings, bullets, escaping — and that code spans/fences are byte-exact.
func TestMarkdownToMrkdwn(t *testing.T) {
	cases := map[string]string{
		"this is **bold** text":        "this is *bold* text",
		"~~gone~~":                     "~gone~",
		"[docs](https://x.io/a?b=1&c)": "<https://x.io/a?b=1&amp;c|docs>",
		"# Title":                      "*Title*",
		"## Sub heading":               "*Sub heading*",
		"- item one\n- item two":       "• item one\n• item two",
		"a < b & c > d":                "a &lt; b &amp; c &gt; d",
		"_italic_ stays":               "_italic_ stays", // mrkdwn native
	}
	for in, want := range cases {
		if got := markdownToMrkdwn(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

// TestMrkdwnCodeProtected: transforms and escaping never touch code.
func TestMrkdwnCodeProtected(t *testing.T) {
	in := "run `a && b < c` and:\n```\n**not bold** [x](y) & <z>\n```\ndone **yes**"
	got := markdownToMrkdwn(in)
	if !strings.Contains(got, "`a && b < c`") {
		t.Errorf("inline code mangled: %q", got)
	}
	if !strings.Contains(got, "**not bold** [x](y) & <z>") {
		t.Errorf("fence contents mangled: %q", got)
	}
	if !strings.Contains(got, "done *yes*") {
		t.Errorf("markup outside code should transform: %q", got)
	}
}

// TestChunk: under-limit passes through; long text splits on newlines.
func TestChunk(t *testing.T) {
	if got := chunk("short", 100); len(got) != 1 || got[0] != "short" {
		t.Fatalf("short: %v", got)
	}
	long := strings.Repeat("line of text\n", 40)
	parts := chunk(long, 100)
	if len(parts) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(parts))
	}
	for i, p := range parts[:len(parts)-1] {
		if !strings.HasSuffix(p, "\n") {
			t.Errorf("chunk %d should end at a newline: %q", i, p[len(p)-10:])
		}
	}
	if strings.Join(parts, "") != long {
		t.Error("chunks must reassemble to the original")
	}
}
