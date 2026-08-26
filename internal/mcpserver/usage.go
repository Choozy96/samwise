package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"samwise/internal/store"
)

// usage_report gives the AGENT access to platform-wide token/cost data — the
// same numbers as the Admin page's Usage & cost panel. Strictly admin-only: the
// binding's user (server-side, unspoofable) must be an admin.

type usageReportIn struct {
	Days    int    `json:"days,omitempty" jsonschema:"how many days back to aggregate (1-90; default 7)"`
	GroupBy string `json:"group_by,omitempty" jsonschema:"how to break the numbers down: 'user' (default), 'model', 'both', or 'total'"`
}

func (h *handlers) registerUsage(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "usage_report",
		Description: "ADMIN ONLY: token usage and cost across ALL users of this platform, from the runs log — " +
			"broken down by user, model, both, or as one total, over the last N days. Use when the admin asks " +
			"about usage, spend, or which user/model is consuming the most. Refused for non-admin users.",
	}, h.usageReport)
}

func (h *handlers) usageReport(ctx context.Context, _ *mcp.CallToolRequest, in usageReportIn) (*mcp.CallToolResult, any, error) {
	// Admin gate on the server-side bound user — the model can't claim adminship.
	caller, err := h.db.GetUserByID(ctx, h.userID)
	if err != nil || caller == nil || !caller.IsAdmin {
		h.audit("usage_report", "", "denied")
		return h.fail("usage_report", "", "this tool is for admin users only"), nil, nil
	}

	days := in.Days
	if days <= 0 {
		days = 7
	}
	if days > 90 {
		days = 90
	}
	group := ""
	switch strings.ToLower(strings.TrimSpace(in.GroupBy)) {
	case "", "user", "users":
		group = "user"
	case "model", "models":
		group = "model"
	case "both", "user_model":
		group = "user_model"
	case "total":
		group = ""
	default:
		return h.fail("usage_report", in.GroupBy, "group_by must be 'user', 'model', 'both', or 'total'"), nil, nil
	}

	from := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Format("2006-01-02 15:04:05")
	rows, err := h.db.UsageReport(ctx, from, "", group)
	if err != nil {
		return h.fail("usage_report", "", err.Error()), nil, nil
	}
	h.audit("usage_report", fmt.Sprintf("days=%d group=%s", days, group), "ok")
	if len(rows) == 0 {
		return textResult(fmt.Sprintf("No runs in the last %dd.", days)), nil, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Usage — last %dd (runs · input · output · cache-write · cache-read · cost USD):\n", days)
	var total store.UsageRow
	for _, u := range rows {
		name := "total"
		switch group {
		case "user":
			name = u.Username
		case "model":
			name = orDefault(u.Model)
		case "user_model":
			name = u.Username + " · " + orDefault(u.Model)
		}
		fmt.Fprintf(&b, "- %s: %d · %s · %s · %s · %s · $%.4f\n", name, u.Runs,
			store.HumanTokens(u.InputTokens), store.HumanTokens(u.OutputTokens),
			store.HumanTokens(u.CacheWrite), store.HumanTokens(u.CacheRead), u.CostUSD)
		total.Runs += u.Runs
		total.InputTokens += u.InputTokens
		total.OutputTokens += u.OutputTokens
		total.CacheWrite += u.CacheWrite
		total.CacheRead += u.CacheRead
		total.CostUSD += u.CostUSD
	}
	if group != "" && len(rows) > 1 {
		fmt.Fprintf(&b, "TOTAL: %d · %s · %s · %s · %s · $%.4f\n", total.Runs,
			store.HumanTokens(total.InputTokens), store.HumanTokens(total.OutputTokens),
			store.HumanTokens(total.CacheWrite), store.HumanTokens(total.CacheRead), total.CostUSD)
	}
	b.WriteString("(cost is runtime-reported — indicative under a subscription)")
	return textResult(strings.TrimRight(b.String(), "\n")), nil, nil
}

func orDefault(model string) string {
	if model == "" {
		return "(default)"
	}
	return model
}
