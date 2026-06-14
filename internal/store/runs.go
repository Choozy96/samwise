package store

import "context"

// Run records one agent execution (spec §11: runs table records runtime, model,
// status, duration, error per run).
type Run struct {
	ID             int64
	UserID         int64
	ConversationID int64
	Runtime        string
	Model          string
	Status         string
	Error          string
	DurationMS     int64
	CostUSD        float64
	StartedAt      string
	FinishedAt     string
}

// CreateRun inserts a run in the "running" state and returns its id.
func (db *DB) CreateRun(ctx context.Context, userID, conversationID int64, runtime, model string) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO runs(user_id, conversation_id, runtime, model, status)
		 VALUES(?,?,?,?,'running')`, userID, conversationID, runtime, model)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishRun marks a run success/error with its metrics.
func (db *DB) FinishRun(ctx context.Context, runID int64, status, errMsg string, durationMS int64, costUSD float64) error {
	_, err := db.ExecContext(ctx,
		`UPDATE runs SET status = ?, error = ?, duration_ms = ?, cost_usd = ?, finished_at = datetime('now')
		  WHERE id = ?`, status, errMsg, durationMS, costUSD, runID)
	return err
}

// RecentRuns returns the last n runs for a user (admin/observability).
func (db *DB) RecentRuns(ctx context.Context, userID int64, n int) ([]Run, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, user_id, conversation_id, runtime, model, status, error,
		        duration_ms, cost_usd, started_at, COALESCE(finished_at,'')
		   FROM runs WHERE user_id = ? ORDER BY id DESC LIMIT ?`, userID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.UserID, &r.ConversationID, &r.Runtime, &r.Model,
			&r.Status, &r.Error, &r.DurationMS, &r.CostUSD, &r.StartedAt, &r.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Usage aggregates a user's runs over a window.
type Usage struct {
	Runs    int
	CostUSD float64
	Errors  int
}

// UsageSince returns run counts, total cost, and error count for a user since the
// given UTC timestamp ('YYYY-MM-DD HH:MM:SS').
func (db *DB) UsageSince(ctx context.Context, userID int64, sinceUTC string) (Usage, error) {
	var u Usage
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(cost_usd),0),
		        COALESCE(SUM(CASE WHEN status='error' THEN 1 ELSE 0 END),0)
		   FROM runs WHERE user_id = ? AND started_at >= ?`, userID, sinceUTC).
		Scan(&u.Runs, &u.CostUSD, &u.Errors)
	return u, err
}
