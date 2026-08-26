// Package discord implements the Discord channel: a Gateway (WebSocket)
// session per bot for inbound messages, a pairing flow, and an outbound
// ChannelSender — the Discord mirror of internal/slack. discordgo is used for
// transport/types only; all Samwise policy lives here.
//
// Formatting is nearly passthrough: Discord natively renders the markdown the
// agent produces (**bold**, *italic*, ~~strike~~, `code`, fences, # headings,
// and masked [text](url) links from bots) — so unlike Telegram/Slack there is
// no conversion step, only chunking at Discord's 2000-char message limit.
package discord

// discordMaxLen is Discord's message-length ceiling.
const discordMaxLen = 2000

// chunk splits text into <=max-rune pieces, preferring newline boundaries —
// same policy as the Telegram/Slack chunkers.
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

// snippet truncates s to at most max runes for quoted reply context.
func snippet(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
