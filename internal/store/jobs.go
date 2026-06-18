package store

import (
	"context"
	"database/sql"
	"errors"
)

// Job is a scheduled job.
type Job struct {
	ID              int64
	UserID          int64
	Name            string
	Type            string // agent_run | direct_message | maintenance
	ScheduleSpec    string
	TZMode          string // user_local | fixed_tz | fixed_utc
	TZRef           string
	Payload         string // JSON
	Enabled         bool
	CatchUp         bool
	NextFireUTC     string // RFC3339 UTC, or ""
	LastFiredPeriod string
}

// CreateJob inserts a job and returns its id.
func (db *DB) CreateJob(ctx context.Context, j Job) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO jobs(user_id, name, type, schedule_spec, tz_mode, tz_ref, payload,
		                  enabled, catch_up, next_fire_utc, last_fired_period)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		j.UserID, j.Name, j.Type, j.ScheduleSpec, j.TZMode, j.TZRef, j.Payload,
		boolToInt(j.Enabled), boolToInt(j.CatchUp), nullStr(j.NextFireUTC), j.LastFiredPeriod)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetJob loads a job by id, scoped to the owning user.
func (db *DB) GetJob(ctx context.Context, userID, id int64) (*Job, error) {
	return scanJob(db.QueryRowContext(ctx, jobSelect+` WHERE id = ? AND user_id = ?`, id, userID))
}

// ListJobs returns all of a user's jobs (portal jobs page).
func (db *DB) ListJobs(ctx context.Context, userID int64) ([]Job, error) {
	return db.queryJobs(ctx, jobSelect+` WHERE user_id = ? ORDER BY id`, userID)
}

// ListUserLocalJobs returns a user's enabled jobs that drift with their
// timezone (tz_mode='user_local') — the set recomputed on a timezone change.
func (db *DB) ListUserLocalJobs(ctx context.Context, userID int64) ([]Job, error) {
	return db.queryJobs(ctx,
		jobSelect+` WHERE user_id = ? AND enabled = 1 AND tz_mode = 'user_local'`, userID)
}

// DueJobs returns all enabled jobs whose materialized fire time is at or before
// nowRFC3339 (UTC). RFC3339 UTC strings sort lexicographically by time.
func (db *DB) DueJobs(ctx context.Context, nowRFC3339 string) ([]Job, error) {
	return db.queryJobs(ctx,
		jobSelect+` WHERE enabled = 1 AND next_fire_utc IS NOT NULL AND next_fire_utc <= ?
		            ORDER BY next_fire_utc`, nowRFC3339)
}

// UpdateJobSchedule writes the recomputed fire time and last-fired period.
func (db *DB) UpdateJobSchedule(ctx context.Context, id int64, nextFireUTC, lastFiredPeriod string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE jobs SET next_fire_utc = ?, last_fired_period = ? WHERE id = ?`,
		nullStr(nextFireUTC), lastFiredPeriod, id)
	return err
}

// SetJobEnabled enables/disables a job (e.g. one-shot completion, user cancel).
func (db *DB) SetJobEnabled(ctx context.Context, userID, id int64, enabled bool) error {
	_, err := db.ExecContext(ctx,
		`UPDATE jobs SET enabled = ? WHERE id = ? AND user_id = ?`, boolToInt(enabled), id, userID)
	return err
}

// UpdateJob updates a job's editable fields (name, schedule, payload, enabled,
// materialized next fire) for a job the user owns. Returns ErrNotFound if no row
// matched. The caller recomputes NextFireUTC when the schedule changes.
func (db *DB) UpdateJob(ctx context.Context, j Job) error {
	res, err := db.ExecContext(ctx,
		`UPDATE jobs SET name = ?, schedule_spec = ?, payload = ?, enabled = ?, next_fire_utc = ?
		 WHERE id = ? AND user_id = ?`,
		j.Name, j.ScheduleSpec, j.Payload, boolToInt(j.Enabled), nullStr(j.NextFireUTC), j.ID, j.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteJob removes a job the user owns.
func (db *DB) DeleteJob(ctx context.Context, userID, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM jobs WHERE id = ? AND user_id = ?`, id, userID)
	return err
}

const jobSelect = `SELECT id, user_id, name, type, schedule_spec, tz_mode, tz_ref, payload,
	enabled, catch_up, COALESCE(next_fire_utc,''), last_fired_period FROM jobs`

func (db *DB) queryJobs(ctx context.Context, query string, args ...any) ([]Job, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJobRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func scanJob(row *sql.Row) (*Job, error) {
	var j Job
	var enabled, catchUp int
	err := row.Scan(&j.ID, &j.UserID, &j.Name, &j.Type, &j.ScheduleSpec, &j.TZMode, &j.TZRef,
		&j.Payload, &enabled, &catchUp, &j.NextFireUTC, &j.LastFiredPeriod)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	j.Enabled = enabled != 0
	j.CatchUp = catchUp != 0
	return &j, nil
}

func scanJobRows(rows *sql.Rows) (Job, error) {
	var j Job
	var enabled, catchUp int
	err := rows.Scan(&j.ID, &j.UserID, &j.Name, &j.Type, &j.ScheduleSpec, &j.TZMode, &j.TZRef,
		&j.Payload, &enabled, &catchUp, &j.NextFireUTC, &j.LastFiredPeriod)
	j.Enabled = enabled != 0
	j.CatchUp = catchUp != 0
	return j, err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
