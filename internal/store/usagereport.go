package store

import (
	"context"
	"fmt"
)

// HumanTokens renders a token count compactly (830, 12.4k, 1.3M) for reports.
func HumanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// UsageRow is one aggregated line of the admin usage report. Username and Model
// are filled according to the grouping ("" when not grouped by that dimension).
type UsageRow struct {
	UserID       int64
	Username     string
	Model        string
	Runs         int64
	InputTokens  int64
	OutputTokens int64
	CacheWrite   int64
	CacheRead    int64
	CostUSD      float64
}

// UsageReport aggregates runs between fromUTC (inclusive) and toUTC (exclusive;
// "" = now) — timestamps in SQLite 'YYYY-MM-DD HH:MM:SS' UTC format — grouped by
// "user", "model", "user_model", or "" (one total row). Admin-only data; the
// caller enforces authorization.
func (db *DB) UsageReport(ctx context.Context, fromUTC, toUTC, groupBy string) ([]UsageRow, error) {
	sel, group := "", ""
	switch groupBy {
	case "user":
		sel, group = "r.user_id, COALESCE(u.username,''), ''", "GROUP BY r.user_id"
	case "model":
		sel, group = "0, '', r.model", "GROUP BY r.model"
	case "user_model":
		sel, group = "r.user_id, COALESCE(u.username,''), r.model", "GROUP BY r.user_id, r.model"
	case "":
		sel = "0, '', ''"
	default:
		return nil, fmt.Errorf("usage report: unknown group_by %q", groupBy)
	}
	q := `SELECT ` + sel + `,
	        COUNT(*), SUM(r.input_tokens), SUM(r.output_tokens),
	        SUM(r.cache_creation_tokens), SUM(r.cache_read_tokens), SUM(r.cost_usd)
	   FROM runs r LEFT JOIN users u ON u.id = r.user_id
	  WHERE r.started_at >= ?`
	args := []any{fromUTC}
	if toUTC != "" {
		q += ` AND r.started_at < ?`
		args = append(args, toUTC)
	}
	if group != "" {
		q += " " + group
	}
	q += ` ORDER BY SUM(r.cost_usd) DESC, SUM(r.output_tokens) DESC`

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageRow
	for rows.Next() {
		var r UsageRow
		var in, outT, cw, cr, runs *int64
		var cost *float64
		if err := rows.Scan(&r.UserID, &r.Username, &r.Model, &runs, &in, &outT, &cw, &cr, &cost); err != nil {
			return nil, err
		}
		// SUM() over zero rows yields NULL for the ungrouped total — treat as 0.
		if runs != nil {
			r.Runs = *runs
		}
		if in != nil {
			r.InputTokens = *in
		}
		if outT != nil {
			r.OutputTokens = *outT
		}
		if cw != nil {
			r.CacheWrite = *cw
		}
		if cr != nil {
			r.CacheRead = *cr
		}
		if cost != nil {
			r.CostUSD = *cost
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
