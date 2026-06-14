package store

import "context"

// AuditEntry is one recorded event (spec §10.5). EventType categorizes it
// (tool|auth|message|skill|job); ToolName holds the action/name for that type.
type AuditEntry struct {
	ID           int64
	EventType    string
	RunID        int64
	ToolName     string
	ArgsSummary  string
	ResultStatus string
	TS           string
}

// AddAuditEvent records any audited event. name is the action (tool name,
// channel, job name…); summary is a short, non-sensitive description.
func (db *DB) AddAuditEvent(ctx context.Context, userID, runID int64, eventType, name, summary, status string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO audit_log(user_id, run_id, event_type, tool_name, args_summary, result_status)
		 VALUES(?,?,?,?,?,?)`, userID, nullInt(runID), eventType, name, summary, status)
	return err
}

// AddAudit records a tool call (event_type=tool). Kept for the core MCP server.
func (db *DB) AddAudit(ctx context.Context, userID, runID int64, toolName, argsSummary, resultStatus string) error {
	return db.AddAuditEvent(ctx, userID, runID, "tool", toolName, argsSummary, resultStatus)
}

// ListAudit returns a user's recent events for the portal audit page.
func (db *DB) ListAudit(ctx context.Context, userID int64, limit int) ([]AuditEntry, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, event_type, COALESCE(run_id,0), tool_name, args_summary, result_status, ts
		   FROM audit_log WHERE user_id = ? ORDER BY id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var a AuditEntry
		if err := rows.Scan(&a.ID, &a.EventType, &a.RunID, &a.ToolName, &a.ArgsSummary, &a.ResultStatus, &a.TS); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
