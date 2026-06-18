package store

import "context"

// Run records one agent execution. Token usage is tracked by type; cost_usd is
// stored for reference but isn't the primary metric.
type Run struct {
	ID                  int64
	UserID              int64
	ConversationID      int64
	Runtime             string
	Model               string
	Status              string
	Error               string
	DurationMS          int64
	CostUSD             float64
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
	StartedAt           string
	FinishedAt          string
}

// RunMetrics are the per-run measurements recorded when a run finishes.
type RunMetrics struct {
	DurationMS          int64
	CostUSD             float64
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
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

// FinishRun marks a run success/error with its metrics (duration, token usage,
// and the runtime-reported cost).
func (db *DB) FinishRun(ctx context.Context, runID int64, status, errMsg string, m RunMetrics) error {
	_, err := db.ExecContext(ctx,
		`UPDATE runs SET status = ?, error = ?, duration_ms = ?, cost_usd = ?,
		        input_tokens = ?, output_tokens = ?, cache_creation_tokens = ?, cache_read_tokens = ?,
		        finished_at = datetime('now')
		  WHERE id = ?`,
		status, errMsg, m.DurationMS, m.CostUSD,
		m.InputTokens, m.OutputTokens, m.CacheCreationTokens, m.CacheReadTokens, runID)
	return err
}

// RecentRuns returns the last n runs for a user (admin/observability).
func (db *DB) RecentRuns(ctx context.Context, userID int64, n int) ([]Run, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, user_id, conversation_id, runtime, model, status, error,
		        duration_ms, cost_usd, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
		        started_at, COALESCE(finished_at,'')
		   FROM runs WHERE user_id = ? ORDER BY id DESC LIMIT ?`, userID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.UserID, &r.ConversationID, &r.Runtime, &r.Model,
			&r.Status, &r.Error, &r.DurationMS, &r.CostUSD,
			&r.InputTokens, &r.OutputTokens, &r.CacheCreationTokens, &r.CacheReadTokens,
			&r.StartedAt, &r.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Usage aggregates a user's runs over a window: run/error counts and token totals
// by type. CostUSD is summed too (for reference) but tokens are the headline.
type Usage struct {
	Runs                int
	Errors              int
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
	CostUSD             float64
}

// UsageSince returns run counts, errors, and token totals for a user since the
// given UTC timestamp ('YYYY-MM-DD HH:MM:SS').
func (db *DB) UsageSince(ctx context.Context, userID int64, sinceUTC string) (Usage, error) {
	var u Usage
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*),
		        COALESCE(SUM(CASE WHEN status='error' THEN 1 ELSE 0 END),0),
		        COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		        COALESCE(SUM(cache_creation_tokens),0), COALESCE(SUM(cache_read_tokens),0),
		        COALESCE(SUM(cost_usd),0)
		   FROM runs WHERE user_id = ? AND started_at >= ?`, userID, sinceUTC).
		Scan(&u.Runs, &u.Errors, &u.InputTokens, &u.OutputTokens,
			&u.CacheCreationTokens, &u.CacheReadTokens, &u.CostUSD)
	return u, err
}
