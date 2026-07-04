package mcpserver

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// update_settings lets the agent change the user's simple preferences (the same
// ones behind /delivery, /format, /groupreply). Model and access method are
// per-persona, set via agent_update, so they're deliberately not here. Gated to
// registered (paired) users.

type updateSettingsIn struct {
	DeliveryChannel string `json:"delivery_channel,omitempty" jsonschema:"default destination for scheduled results: 'web' or 'telegram'"`
	MessageFormat   string `json:"message_format,omitempty" jsonschema:"Telegram message formatting: 'markdown', 'html', or 'plain'"`
	GroupReply      string `json:"group_reply,omitempty" jsonschema:"in group chats, reply only when addressed ('mention') or to every message ('all')"`
}

func (h *handlers) registerSelfConfig(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_settings",
		Description: "Change the user's preferences: default delivery channel (web/telegram), Telegram message format (markdown/html/plain), and group reply mode (mention/all). Only the fields you provide change. To change the model or access method, use agent_update on the agent instead.",
	}, h.updateSettings)
}

func (h *handlers) updateSettings(ctx context.Context, _ *mcp.CallToolRequest, in updateSettingsIn) (*mcp.CallToolResult, any, error) {
	if h.readOnly {
		return h.denyWrite("update_settings"), nil, nil
	}
	s, err := h.db.GetSettings(ctx, h.userID)
	if err != nil {
		return h.fail("update_settings", "", err.Error()), nil, nil
	}
	var changes []string
	if v := strings.ToLower(strings.TrimSpace(in.DeliveryChannel)); v != "" {
		if v != "web" && v != "telegram" {
			return h.fail("update_settings", "delivery_channel="+v, "delivery_channel must be 'web' or 'telegram'"), nil, nil
		}
		s.DeliveryChannel = v
		changes = append(changes, "delivery channel → "+v)
	}
	if v := strings.ToLower(strings.TrimSpace(in.MessageFormat)); v != "" {
		if v != "markdown" && v != "html" && v != "plain" {
			return h.fail("update_settings", "message_format="+v, "message_format must be 'markdown', 'html', or 'plain'"), nil, nil
		}
		s.TgFormat = v
		changes = append(changes, "message format → "+v)
	}
	if v := strings.ToLower(strings.TrimSpace(in.GroupReply)); v != "" {
		if v != "mention" && v != "all" {
			return h.fail("update_settings", "group_reply="+v, "group_reply must be 'mention' or 'all'"), nil, nil
		}
		s.GroupReplyMode = v
		changes = append(changes, "group reply → "+v)
	}
	if len(changes) == 0 {
		return textResult("Nothing to change — pass delivery_channel, message_format, and/or group_reply."), nil, nil
	}
	if err := h.db.UpdateSettings(ctx, s); err != nil {
		return h.fail("update_settings", "", err.Error()), nil, nil
	}
	h.audit("update_settings", strings.Join(changes, "; "), "ok")
	return textResult("Updated: " + strings.Join(changes, ", ") + "."), nil, nil
}
