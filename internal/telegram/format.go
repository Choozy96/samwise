package telegram

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// Telegram message formats. FormatMarkdown (default) converts the agent's
// CommonMark to Telegram MarkdownV2 and sends it with parse_mode=MarkdownV2;
// FormatHTML converts to Telegram's HTML subset and sends parse_mode=HTML. Both
// fall back to plain text if Telegram rejects the markup, so a message always
// gets through. A "plain" format sends text as-is (no parse_mode). The old
// "telegram" value now maps to markdown (the new default).
const (
	FormatMarkdown = "markdown"
	FormatHTML     = "html"
	FormatPlain    = "plain"
)

// isHTMLFormat reports whether a stored format value selects the HTML path.
func isHTMLFormat(format string) bool {
	return format == FormatHTML
}

var mdParser = goldmark.New(goldmark.WithExtensions(extension.Strikethrough, extension.Linkify)).Parser()

// markdownToTelegramHTML converts CommonMark to the small HTML subset Telegram
// renders with parse_mode=HTML. Constructs Telegram has no tag for (headings,
// lists, rules) are mapped to bold/bullets/dashes; raw HTML is escaped to text so
// we never emit a tag Telegram would reject.
func markdownToTelegramHTML(md string) string {
	src := []byte(md)
	doc := mdParser.Parse(text.NewReader(src))
	var b strings.Builder
	renderTG(&b, doc, src)
	return strings.TrimSpace(b.String())
}

func tgEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func tgChildren(b *strings.Builder, n ast.Node, src []byte) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		renderTG(b, c, src)
	}
}

// tgPlain emits the escaped text of a subtree with no formatting tags (for code
// spans and link labels).
func tgPlain(b *strings.Builder, n ast.Node, src []byte) {
	switch t := n.(type) {
	case *ast.Text:
		b.WriteString(tgEscape(string(t.Segment.Value(src))))
	case *ast.String:
		b.WriteString(tgEscape(string(t.Value)))
	default:
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			tgPlain(b, c, src)
		}
	}
}

func tgLines(b *strings.Builder, n ast.Node, src []byte) {
	l := n.Lines()
	for i := 0; i < l.Len(); i++ {
		seg := l.At(i)
		b.WriteString(tgEscape(string(seg.Value(src))))
	}
}

func renderTG(b *strings.Builder, n ast.Node, src []byte) {
	switch n := n.(type) {
	case *ast.Document:
		tgChildren(b, n, src)
	case *ast.Heading:
		b.WriteString("<b>")
		tgChildren(b, n, src)
		b.WriteString("</b>\n\n")
	case *ast.Paragraph:
		tgChildren(b, n, src)
		b.WriteString("\n\n")
	case *ast.TextBlock:
		tgChildren(b, n, src)
	case *ast.Text:
		b.WriteString(tgEscape(string(n.Segment.Value(src))))
		if n.SoftLineBreak() || n.HardLineBreak() {
			b.WriteString("\n")
		}
	case *ast.String:
		b.WriteString(tgEscape(string(n.Value)))
	case *ast.Emphasis:
		tag := "i"
		if n.Level >= 2 {
			tag = "b"
		}
		b.WriteString("<" + tag + ">")
		tgChildren(b, n, src)
		b.WriteString("</" + tag + ">")
	case *east.Strikethrough:
		b.WriteString("<s>")
		tgChildren(b, n, src)
		b.WriteString("</s>")
	case *ast.CodeSpan:
		b.WriteString("<code>")
		tgPlain(b, n, src)
		b.WriteString("</code>")
	case *ast.FencedCodeBlock:
		b.WriteString("<pre>")
		tgLines(b, n, src)
		b.WriteString("</pre>\n")
	case *ast.CodeBlock:
		b.WriteString("<pre>")
		tgLines(b, n, src)
		b.WriteString("</pre>\n")
	case *ast.Link:
		b.WriteString(`<a href="` + tgEscape(string(n.Destination)) + `">`)
		tgChildren(b, n, src)
		b.WriteString("</a>")
	case *ast.AutoLink:
		u := string(n.URL(src))
		b.WriteString(`<a href="` + tgEscape(u) + `">` + tgEscape(u) + "</a>")
	case *ast.List:
		tgChildren(b, n, src)
		b.WriteString("\n")
	case *ast.ListItem:
		b.WriteString("• ")
		tgChildren(b, n, src)
		b.WriteString("\n")
	case *ast.Blockquote:
		b.WriteString("<blockquote>")
		tgChildren(b, n, src)
		b.WriteString("</blockquote>\n")
	case *ast.ThematicBreak:
		b.WriteString("———\n")
	case *ast.RawHTML:
		for i := 0; i < n.Segments.Len(); i++ {
			seg := n.Segments.At(i)
			b.WriteString(tgEscape(string(seg.Value(src))))
		}
	case *ast.HTMLBlock:
		tgLines(b, n, src)
	default:
		tgChildren(b, n, src)
	}
}

// ── MarkdownV2 ───────────────────────────────────────────────────────────────

// mdv2Special are the characters Telegram MarkdownV2 requires be backslash-escaped
// in ordinary text (outside entities). Missing even one makes Telegram reject the
// whole message, so we escape every text node.
const mdv2Special = "_*[]()~`>#+-=|{}.!"

// mdEscape escapes ordinary MarkdownV2 text.
func mdEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if strings.ContainsRune(mdv2Special, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// mdCodeEscape escapes the content of a code span / code block (only backslash
// and backtick are special inside code entities).
func mdCodeEscape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "`", "\\`")
	return s
}

// mdURLEscape escapes a link destination (only backslash and ')' are special
// inside the (...) of an inline link).
func mdURLEscape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, ")", "\\)")
	return s
}

// markdownToMarkdownV2 converts CommonMark to Telegram MarkdownV2. Constructs
// Telegram lacks (headings, lists, rules) map to bold/bullets/dashes; all text is
// escaped so the message is always valid MarkdownV2.
func markdownToMarkdownV2(md string) string {
	src := []byte(md)
	doc := mdParser.Parse(text.NewReader(src))
	var b strings.Builder
	renderMD(&b, doc, src)
	return strings.TrimSpace(b.String())
}

func mdChildren(b *strings.Builder, n ast.Node, src []byte) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		renderMD(b, c, src)
	}
}

// mdPlainCode emits the escaped text of a code subtree (code spans hold their
// text in child Text nodes).
func mdCodeChildren(b *strings.Builder, n ast.Node, src []byte) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if t, ok := c.(*ast.Text); ok {
			b.WriteString(mdCodeEscape(string(t.Segment.Value(src))))
		}
	}
}

func mdLines(b *strings.Builder, n ast.Node, src []byte) {
	l := n.Lines()
	for i := 0; i < l.Len(); i++ {
		seg := l.At(i)
		b.WriteString(mdCodeEscape(string(seg.Value(src))))
	}
}

func renderMD(b *strings.Builder, n ast.Node, src []byte) {
	switch n := n.(type) {
	case *ast.Document:
		mdChildren(b, n, src)
	case *ast.Heading:
		b.WriteString("*")
		mdChildren(b, n, src)
		b.WriteString("*\n\n")
	case *ast.Paragraph:
		mdChildren(b, n, src)
		b.WriteString("\n\n")
	case *ast.TextBlock:
		mdChildren(b, n, src)
	case *ast.Text:
		b.WriteString(mdEscape(string(n.Segment.Value(src))))
		if n.SoftLineBreak() || n.HardLineBreak() {
			b.WriteString("\n")
		}
	case *ast.String:
		b.WriteString(mdEscape(string(n.Value)))
	case *ast.Emphasis:
		// MarkdownV2: *bold*, _italic_.
		delim := "_"
		if n.Level >= 2 {
			delim = "*"
		}
		b.WriteString(delim)
		mdChildren(b, n, src)
		b.WriteString(delim)
	case *east.Strikethrough:
		b.WriteString("~")
		mdChildren(b, n, src)
		b.WriteString("~")
	case *ast.CodeSpan:
		b.WriteString("`")
		mdCodeChildren(b, n, src)
		b.WriteString("`")
	case *ast.FencedCodeBlock:
		b.WriteString("```\n")
		mdLines(b, n, src)
		b.WriteString("```\n")
	case *ast.CodeBlock:
		b.WriteString("```\n")
		mdLines(b, n, src)
		b.WriteString("```\n")
	case *ast.Link:
		b.WriteString("[")
		mdChildren(b, n, src)
		b.WriteString("](" + mdURLEscape(string(n.Destination)) + ")")
	case *ast.AutoLink:
		u := string(n.URL(src))
		b.WriteString("[" + mdEscape(u) + "](" + mdURLEscape(u) + ")")
	case *ast.List:
		mdChildren(b, n, src)
		b.WriteString("\n")
	case *ast.ListItem:
		b.WriteString("• ")
		mdChildren(b, n, src)
		b.WriteString("\n")
	case *ast.Blockquote:
		// MarkdownV2 blockquotes prefix each line with '>'.
		var inner strings.Builder
		mdChildren(&inner, n, src)
		for _, line := range strings.Split(strings.TrimRight(inner.String(), "\n"), "\n") {
			b.WriteString(">" + line + "\n")
		}
	case *ast.ThematicBreak:
		b.WriteString("———\n")
	case *ast.RawHTML:
		for i := 0; i < n.Segments.Len(); i++ {
			seg := n.Segments.At(i)
			b.WriteString(mdEscape(string(seg.Value(src))))
		}
	case *ast.HTMLBlock:
		var raw strings.Builder
		l := n.Lines()
		for i := 0; i < l.Len(); i++ {
			seg := l.At(i)
			raw.WriteString(string(seg.Value(src)))
		}
		b.WriteString(mdEscape(raw.String()))
	default:
		mdChildren(b, n, src)
	}
}

// ── delivery ────────────────────────────────────────────────────────────────

// deliver chunks rawText and sends each piece in the user's format. markdown =>
// MarkdownV2, html => HTML; if Telegram rejects the formatted markup it resends
// that chunk as plain text so the message always gets through.
func deliver(ctx context.Context, c *Client, chatID int64, rawText, format string, log *slog.Logger) error {
	size := tgMaxLen
	if format != FormatPlain {
		size = 3500 // leave headroom: escapes/tags can push a chunk past tgMaxLen
	}
	for _, raw := range chunk(rawText, size) {
		rendered, mode := raw, ""
		if isHTMLFormat(format) {
			rendered, mode = markdownToTelegramHTML(raw), "HTML"
		} else if format != FormatPlain {
			rendered, mode = markdownToMarkdownV2(raw), "MarkdownV2"
		}
		if mode != "" {
			if err := c.SendMessage(ctx, chatID, rendered, mode); err == nil {
				continue
			} else if log != nil {
				log.Warn("telegram: formatted send failed, sending plain", "mode", mode, "err", err)
			}
		}
		// Plain fallback: the raw markdown text, no parse_mode.
		if err := sendRetry(ctx, c, chatID, raw); err != nil {
			return err
		}
	}
	return nil
}

// sendRetry sends a plain chunk, retrying transient failures.
func sendRetry(ctx context.Context, c *Client, chatID int64, text string) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = c.SendMessage(ctx, chatID, text, ""); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
	}
	return err
}
