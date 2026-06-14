package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDeliverSetsParseMode is the regression test for the formatting bug: a
// formatted send must include the right parse_mode parameter (else Telegram shows
// the markup literally), and plain must omit it.
func TestDeliverSetsParseMode(t *testing.T) {
	var gotMode, gotText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text      string `json:"text"`
			ParseMode string `json:"parse_mode"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotMode, gotText = body.ParseMode, body.Text
		io.WriteString(w, `{"ok":true,"result":{}}`)
	}))
	defer srv.Close()
	c := &Client{token: "t", baseURL: srv.URL + "/bott", http: srv.Client()}
	ctx := context.Background()

	if err := deliver(ctx, c, 1, "this is **bold**", FormatMarkdown, nil); err != nil {
		t.Fatal(err)
	}
	if gotMode != "MarkdownV2" || !strings.Contains(gotText, "*bold*") {
		t.Errorf("markdown: mode=%q text=%q (want MarkdownV2 + *bold*)", gotMode, gotText)
	}

	if err := deliver(ctx, c, 1, "this is **bold**", FormatHTML, nil); err != nil {
		t.Fatal(err)
	}
	if gotMode != "HTML" || !strings.Contains(gotText, "<b>bold</b>") {
		t.Errorf("html: mode=%q text=%q (want HTML + <b>bold</b>)", gotMode, gotText)
	}

	if err := deliver(ctx, c, 1, "this is **bold**", FormatPlain, nil); err != nil {
		t.Fatal(err)
	}
	if gotMode != "" || gotText != "this is **bold**" {
		t.Errorf("plain: mode=%q text=%q (want no mode + raw text)", gotMode, gotText)
	}
}

func TestMarkdownToTelegramHTML(t *testing.T) {
	cases := []struct {
		name string
		md   string
		want []string // substrings that must appear
		not  []string // substrings that must NOT appear
	}{
		{"bold", "this is **bold** text", []string{"<b>bold</b>"}, []string{"**"}},
		{"italic", "this is *italic*", []string{"<i>italic</i>"}, []string{"*italic*"}},
		{"inline code", "run `go build` now", []string{"<code>go build</code>"}, []string{"`"}},
		{"link", "see [docs](https://example.com)", []string{`<a href="https://example.com">docs</a>`}, nil},
		{"heading -> bold", "# Title\n\nbody", []string{"<b>Title</b>"}, []string{"#"}},
		{"bullet list", "- one\n- two", []string{"• one", "• two"}, []string{"- one"}},
		{"strikethrough", "~~gone~~", []string{"<s>gone</s>"}, nil},
		{"escape specials", "a < b & c > d", []string{"&lt;", "&amp;", "&gt;"}, []string{"a < b"}},
		{"escape inside code", "`x<y&z>`", []string{"<code>x&lt;y&amp;z&gt;</code>"}, nil},
		{"code block", "```\nplain & <stuff>\n```", []string{"<pre>", "&lt;stuff&gt;", "</pre>"}, nil},
		{"raw html neutralized", "type <script>alert(1)</script>", []string{"&lt;script&gt;"}, []string{"<script>"}},
	}
	for _, c := range cases {
		got := markdownToTelegramHTML(c.md)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q missing %q", c.name, got, w)
			}
		}
		for _, n := range c.not {
			if strings.Contains(got, n) {
				t.Errorf("%s: %q should not contain %q", c.name, got, n)
			}
		}
	}
}

// TestMarkdownPassthroughNotConverted: the converter must produce valid escaped
// output even for tricky input (no panics, no unescaped angle brackets outside
// our tags). Light smoke check.
func TestTelegramHTMLNoStrayAngles(t *testing.T) {
	got := markdownToTelegramHTML("compare a<b and c>d in `m<n`")
	// Every literal angle from the source must be escaped.
	if strings.Contains(got, "a<b") || strings.Contains(got, "c>d") || strings.Contains(got, "m<n") {
		t.Errorf("unescaped source angles leaked: %q", got)
	}
}

func TestMarkdownToMarkdownV2(t *testing.T) {
	cases := []struct {
		name string
		md   string
		want []string // substrings that must appear
		not  []string // substrings that must NOT appear
	}{
		{"bold", "this is **bold** text", []string{"*bold*"}, []string{"**bold**"}},
		{"italic", "this is *italic*", []string{"_italic_"}, nil},
		{"inline code", "run `go build` now", []string{"`go build`"}, nil},
		{"link", "see [docs](https://example.com)", []string{"[docs](https://example.com)"}, nil},
		{"heading -> bold", "# Title\n\nbody", []string{"*Title*"}, nil},
		{"bullet list", "- one\n- two", []string{"• one", "• two"}, []string{"- one"}},
		{"strikethrough", "~~gone~~", []string{"~gone~"}, nil},
		// MarkdownV2 specials in ordinary text must be backslash-escaped.
		{"escape period+bang", "Hello world! See item 1.", []string{"world\\!", "1\\."}, nil},
		{"escape brackets/paren", "a (b) [c]", []string{"\\(b\\)", "\\[c\\]"}, nil},
		{"escape dash/plus", "1 - 2 + 3", []string{"\\-", "\\+"}, nil},
	}
	for _, c := range cases {
		got := markdownToMarkdownV2(c.md)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q missing %q", c.name, got, w)
			}
		}
		for _, n := range c.not {
			if strings.Contains(got, n) {
				t.Errorf("%s: %q should not contain %q", c.name, got, n)
			}
		}
	}
}

// TestMarkdownV2AllSpecialsEscaped verifies that every MarkdownV2 special char in
// plain prose is escaped (an unescaped one makes Telegram reject the whole
// message), while our own entity delimiters stay intact.
func TestMarkdownV2AllSpecialsEscaped(t *testing.T) {
	// Plain text containing each special; none are markdown syntax here.
	got := markdownToMarkdownV2("chars: _ # + - = | { } . ! > ~ and done")
	for _, ch := range []string{"\\_", "\\#", "\\+", "\\-", "\\=", "\\|", "\\{", "\\}", "\\.", "\\!", "\\>", "\\~"} {
		if !strings.Contains(got, ch) {
			t.Errorf("special not escaped (%s) in %q", ch, got)
		}
	}
}
