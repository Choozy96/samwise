package orchestrator

import (
	"strconv"
	"strings"
)

// Address identifies a specific chat on a specific bot of a channel — the one
// way a delivery destination is represented everywhere (job payloads, user
// defaults, the anchor chat, run origins). ChatID is a string because not every
// channel uses numeric ids (Telegram does; Slack uses "C0123…").
type Address struct {
	Channel string // "telegram" (later: "slack", …)
	BotID   int64
	ChatID  string
}

// IsZero reports an unset address.
func (a Address) IsZero() bool { return a.Channel == "" }

// String renders the stored form. Telegram keeps its legacy "tg:" scheme so
// every value already in the DB (job payloads, settings, anchor) stays valid;
// other channels use their name as the scheme (e.g. "slack:2:C0123").
func (a Address) String() string {
	if a.IsZero() {
		return ""
	}
	scheme := a.Channel
	if a.Channel == "telegram" {
		scheme = "tg"
	}
	return scheme + ":" + strconv.FormatInt(a.BotID, 10) + ":" + a.ChatID
}

// ParseAddress parses a stored destination ("tg:<botID>:<chatID>",
// "slack:<botID>:<chatID>", …). ok is false for anything else — including the
// coarse channel prefs "web"/"telegram", which are not addresses.
func ParseAddress(s string) (Address, bool) {
	parts := strings.SplitN(strings.TrimSpace(s), ":", 3)
	if len(parts) != 3 || parts[2] == "" {
		return Address{}, false
	}
	var channel string
	switch parts[0] {
	case "tg", "telegram":
		channel = "telegram"
	case "slack":
		channel = "slack"
	default:
		return Address{}, false
	}
	botID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return Address{}, false
	}
	return Address{Channel: channel, BotID: botID, ChatID: parts[2]}, true
}

// KnownChannels lists the external channels this build supports; the web layer
// iterates it when listing paired chats. Append new channels here.
var KnownChannels = []string{"telegram"}
