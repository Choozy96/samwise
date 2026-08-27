// Package slack implements the Slack channel: Socket Mode inbound (DMs +
// channels), a pairing flow, and an outbound ChannelSender — the Slack mirror
// of internal/telegram. The slack-go library is used for transport/types only;
// all Samwise policy (gating, pairing, formatting, delivery) lives here.
package slack

import (
	"fmt"
	"regexp"
	"strings"
)

// slackMaxLen is Slack's message-length ceiling (~40k); chunk under it.
const slackMaxLen = 39000

// Slack auto-links and mentions use <…>; escape the raw specials first.
var mrkdwnEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

var (
	reFence  = regexp.MustCompile("(?s)```.*?```")
	reCode   = regexp.MustCompile("`[^`\n]+`")
	reLink   = regexp.MustCompile(`\[([^\]\n]+)\]\(([^)\s]+)\)`)
	reBold   = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	reStrike = regexp.MustCompile(`~~([^~\n]+)~~`)
	reHead   = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	reBullet = regexp.MustCompile(`(?m)^(\s*)[-*]\s+`)
)

// markdownToMrkdwn converts the agent's GitHub-flavored markdown to Slack
// mrkdwn: **bold** → *bold*, ~~strike~~ → ~strike~, [t](u) → <u|t>, headings →
// bold lines, "- " bullets → "• ". Code spans and fences are protected from
// every transform (and from escaping) so code stays byte-exact.
func markdownToMrkdwn(md string) string {
	// 1) Pull code out so nothing inside is escaped or rewritten.
	var saved []string
	stash := func(s string) string {
		saved = append(saved, s)
		return fmt.Sprintf("\x00CODE%d\x00", len(saved)-1)
	}
	out := reFence.ReplaceAllStringFunc(md, stash)
	out = reCode.ReplaceAllStringFunc(out, stash)

	// 2) Escape Slack's specials, then apply the markup transforms. (Escaping
	// first is safe for links: it only touches &, <, > — the [](…) syntax
	// survives, and entity-escaped URLs are what Slack expects inside <…|…>.)
	out = mrkdwnEscaper.Replace(out)
	out = reLink.ReplaceAllString(out, "<$2|$1>")
	out = reBold.ReplaceAllString(out, "*$1*")
	out = reStrike.ReplaceAllString(out, "~$1~")
	out = reHead.ReplaceAllString(out, "*$1*")
	out = reBullet.ReplaceAllString(out, "$1• ")

	// 3) Restore code verbatim.
	for i, s := range saved {
		out = strings.Replace(out, fmt.Sprintf("\x00CODE%d\x00", i), s, 1)
	}
	return out
}

// chunk splits text into <=max-rune pieces, preferring newline boundaries —
// same policy as the Telegram chunker.
func chunk(text string, max int) []string {
	if len([]rune(text)) <= max {
		return []string{text}
	}
	var out []string
	runes := []rune(text)
	for len(runes) > 0 {
		if len(runes) <= max {
			out = append(out, string(runes))
			break
		}
		cut := max
		for i := max; i > max/2; i-- {
			if runes[i-1] == '\n' {
				cut = i
				break
			}
		}
		out = append(out, string(runes[:cut]))
		runes = runes[cut:]
	}
	return out
}

// snippet truncates s to at most max runes for quoted context.
func snippet(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
