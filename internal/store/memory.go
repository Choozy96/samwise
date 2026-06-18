package store

import (
	"context"
	"strconv"
	"strings"
)

// SemanticMemory is one discrete fact/preference/event.
type SemanticMemory struct {
	ID        int64
	Topic     string
	Kind      string
	Content   string
	Source    string
	CreatedAt string
	ExpiresAt string
}

// EpisodicMemory is one dated distillation (a daily/weekly summary).
type EpisodicMemory struct {
	ID         int64
	PeriodType string // day | week
	PeriodDate string // 'YYYY-MM-DD'
	Content    string
	CreatedAt  string
}

// MemoryHit is a retrieval result spanning either memory layer.
type MemoryHit struct {
	Layer   string // semantic | episodic
	RefID   int64
	Topic   string
	Kind    string
	Content string
	TS      string
}

// SaveSemantic stores a semantic memory row and returns its id.
func (db *DB) SaveSemantic(ctx context.Context, userID int64, topic, kind, content, source string) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO memory_semantic(user_id, topic, kind, content, source)
		 VALUES(?,?,?,?,?)`, userID, topic, kind, content, source)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SaveEpisodic stores a dated episodic distillation row and returns its id.
func (db *DB) SaveEpisodic(ctx context.Context, userID int64, periodType, periodDate, content string) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO memory_episodic(user_id, period_type, period_date, content)
		 VALUES(?,?,?,?)`, userID, periodType, periodDate, content)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ForgetSemantic deletes a semantic memory the user owns, reporting whether a
// row was removed.
func (db *DB) ForgetSemantic(ctx context.Context, userID, id int64) (bool, error) {
	res, err := db.ExecContext(ctx,
		`DELETE FROM memory_semantic WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeleteSemanticBySource removes a user's semantic memories from a given source
// (e.g. "onboarding"), so re-running onboarding doesn't duplicate them.
func (db *DB) DeleteSemanticBySource(ctx context.Context, userID int64, source string) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM memory_semantic WHERE user_id = ? AND source = ?`, userID, source)
	return err
}

// ListTopics returns the distinct non-empty topics in a user's semantic memory.
func (db *DB) ListTopics(ctx context.Context, userID int64) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT DISTINCT topic FROM memory_semantic
		  WHERE user_id = ? AND topic <> '' ORDER BY topic`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListSemantic returns a user's semantic memories for the portal editor, newest
// first.
func (db *DB) ListSemantic(ctx context.Context, userID int64, limit int) ([]SemanticMemory, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, topic, kind, content, source, created_at, COALESCE(expires_at,'')
		   FROM memory_semantic WHERE user_id = ? ORDER BY id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SemanticMemory
	for rows.Next() {
		var m SemanticMemory
		if err := rows.Scan(&m.ID, &m.Topic, &m.Kind, &m.Content, &m.Source, &m.CreatedAt, &m.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// TopicCount / DateCount are navigation-index rows (a label + how many entries).
type TopicCount struct {
	Topic string
	Count int
}
type DateCount struct {
	Date  string
	Count int
}

// TopicCounts returns every distinct non-empty semantic topic with its entry
// count (accurate at any scale — aggregated in SQL), ordered by topic.
func (db *DB) TopicCounts(ctx context.Context, userID int64) ([]TopicCount, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT topic, COUNT(*) FROM memory_semantic
		  WHERE user_id = ? AND topic <> '' GROUP BY topic ORDER BY topic`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TopicCount
	for rows.Next() {
		var t TopicCount
		if err := rows.Scan(&t.Topic, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// EpisodicDateCounts returns every distinct episodic period_date with its count,
// newest first.
func (db *DB) EpisodicDateCounts(ctx context.Context, userID int64) ([]DateCount, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT period_date, COUNT(*) FROM memory_episodic
		  WHERE user_id = ? GROUP BY period_date ORDER BY period_date DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DateCount
	for rows.Next() {
		var d DateCount
		if err := rows.Scan(&d.Date, &d.Count); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SemanticByTopic returns a page of a topic's semantic memories, newest first.
func (db *DB) SemanticByTopic(ctx context.Context, userID int64, topic string, limit, offset int) ([]SemanticMemory, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, topic, kind, content, source, created_at, COALESCE(expires_at,'')
		   FROM memory_semantic WHERE user_id = ? AND topic = ?
		  ORDER BY id DESC LIMIT ? OFFSET ?`, userID, topic, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SemanticMemory
	for rows.Next() {
		var m SemanticMemory
		if err := rows.Scan(&m.ID, &m.Topic, &m.Kind, &m.Content, &m.Source, &m.CreatedAt, &m.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// EpisodicByDate returns a page of the episodic entries for one date.
func (db *DB) EpisodicByDate(ctx context.Context, userID int64, date string, limit, offset int) ([]EpisodicMemory, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, period_type, period_date, content, created_at
		   FROM memory_episodic WHERE user_id = ? AND period_date = ?
		  ORDER BY id DESC LIMIT ? OFFSET ?`, userID, date, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EpisodicMemory
	for rows.Next() {
		var m EpisodicMemory
		if err := rows.Scan(&m.ID, &m.PeriodType, &m.PeriodDate, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListEpisodic returns a user's dated episodic memories, newest period first.
func (db *DB) ListEpisodic(ctx context.Context, userID int64, limit int) ([]EpisodicMemory, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, period_type, period_date, content, created_at
		   FROM memory_episodic WHERE user_id = ?
		  ORDER BY period_date DESC, id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EpisodicMemory
	for rows.Next() {
		var m EpisodicMemory
		if err := rows.Scan(&m.ID, &m.PeriodType, &m.PeriodDate, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpsertEpisodic stores or replaces the episodic row for a (user, type, date) so
// the day's running summary updates in place. Delete-then-insert keeps the FTS
// index consistent (the table has insert/delete triggers, not necessarily update).
func (db *DB) UpsertEpisodic(ctx context.Context, userID int64, periodType, periodDate, content string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM memory_episodic WHERE user_id=? AND period_type=? AND period_date=?`,
		userID, periodType, periodDate); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO memory_episodic(user_id, period_type, period_date, content) VALUES(?,?,?,?)`,
		userID, periodType, periodDate, content); err != nil {
		return err
	}
	return tx.Commit()
}

// RecentEpisodic returns the user's episodic entries on or after sinceDate
// (inclusive), newest first — used to always-load recent days into context.
func (db *DB) RecentEpisodic(ctx context.Context, userID int64, sinceDate string) ([]EpisodicMemory, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, period_type, period_date, content, created_at
		   FROM memory_episodic WHERE user_id = ? AND period_date >= ?
		  ORDER BY period_date DESC, id DESC`, userID, sinceDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EpisodicMemory
	for rows.Next() {
		var m EpisodicMemory
		if err := rows.Scan(&m.ID, &m.PeriodType, &m.PeriodDate, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ForgetEpisodic deletes a dated episodic memory the user owns.
func (db *DB) ForgetEpisodic(ctx context.Context, userID, id int64) (bool, error) {
	res, err := db.ExecContext(ctx,
		`DELETE FROM memory_episodic WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// SearchMemory runs an FTS5 query across both memory layers, scoped to the user,
// with optional topic and time-range filters, returning up to k ranked hits
//.
func (db *DB) SearchMemory(ctx context.Context, userID int64, query, topic, after, before string, k int) ([]MemoryHit, error) {
	match := buildMatch(query)
	if match == "" || k <= 0 {
		return nil, nil
	}

	// FTS5 preserves the value's storage class: the triggers insert user_id as
	// an INTEGER, so the filter must bind an integer. Binding a string ('1')
	// would never match the stored integer (1) — SQLite treats them as distinct
	// storage classes.
	sql := `SELECT layer, ref_id, topic, kind, content, ts
	          FROM memory_fts
	         WHERE memory_fts MATCH ? AND user_id = ?`
	args := []any{match, userID}
	if topic != "" {
		sql += ` AND topic = ?`
		args = append(args, topic)
	}
	if after != "" {
		sql += ` AND ts >= ?`
		args = append(args, after)
	}
	if before != "" {
		sql += ` AND ts <= ?`
		args = append(args, before)
	}
	sql += ` ORDER BY rank LIMIT ?`
	args = append(args, k)

	rows, err := db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemoryHit
	for rows.Next() {
		var h MemoryHit
		var refID string
		if err := rows.Scan(&h.Layer, &refID, &h.Topic, &h.Kind, &h.Content, &h.TS); err != nil {
			return nil, err
		}
		h.RefID, _ = strconv.ParseInt(refID, 10, 64)
		out = append(out, h)
	}
	return out, rows.Err()
}

// buildMatch turns a free-text query into a safe FTS5 MATCH expression: each
// alphanumeric token is quoted and the tokens are OR-ed for recall. Returns ""
// when the query has no usable tokens (callers then skip the search).
func buildMatch(query string) string {
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return !(r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9'))
	})
	var quoted []string
	for _, f := range fields {
		if len(f) < 2 {
			continue // drop single chars / noise
		}
		quoted = append(quoted, `"`+f+`"`)
	}
	return strings.Join(quoted, " OR ")
}
