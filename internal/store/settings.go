package store

import (
	"context"
	"database/sql"
	"errors"
)

// GetSettings returns a user's settings row, or ErrNotFound.
func (db *DB) GetSettings(ctx context.Context, userID int64) (*Settings, error) {
	var s Settings
	var distillNotify int
	err := db.QueryRowContext(ctx,
		`SELECT user_id, timezone, active_runtime, delivery_channel, model_hints,
		        briefing_time, restart_time, distillation_time,
		        transcript_window_n, retrieval_k, tg_format, distill_notify
		   FROM user_settings WHERE user_id = ?`, userID).
		Scan(&s.UserID, &s.Timezone, &s.ActiveRuntime, &s.DeliveryChannel, &s.ModelHints,
			&s.BriefingTime, &s.RestartTime, &s.DistillationTime,
			&s.TranscriptWindowN, &s.RetrievalK, &s.TgFormat, &distillNotify)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.DistillNotify = distillNotify != 0
	return &s, nil
}

// GetOnboarded reports whether the user has completed first-run setup.
func (db *DB) GetOnboarded(ctx context.Context, userID int64) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT onboarded FROM user_settings WHERE user_id = ?`, userID).Scan(&n)
	return n != 0, err
}

// SetOnboarded marks (or clears) a user's onboarding completion.
func (db *DB) SetOnboarded(ctx context.Context, userID int64, done bool) error {
	v := 0
	if done {
		v = 1
	}
	_, err := db.ExecContext(ctx, `UPDATE user_settings SET onboarded = ? WHERE user_id = ?`, v, userID)
	return err
}

// UpdateTimezone sets just the timezone (used by the set_timezone tool). The
// scheduler layer recomputes affected jobs separately (spec §8.2).
func (db *DB) UpdateTimezone(ctx context.Context, userID int64, tz string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE user_settings SET timezone = ? WHERE user_id = ?`, tz, userID)
	return err
}

// UpdateSettings persists an edited settings row. Timezone changes that must
// recompute scheduled jobs are handled by the scheduler layer (spec §8.2), not
// here — this is a plain write.
func (db *DB) UpdateSettings(ctx context.Context, s *Settings) error {
	_, err := db.ExecContext(ctx,
		`UPDATE user_settings SET
		    timezone = ?, active_runtime = ?, delivery_channel = ?, model_hints = ?,
		    briefing_time = ?, restart_time = ?, distillation_time = ?,
		    transcript_window_n = ?, retrieval_k = ?, tg_format = ?, distill_notify = ?
		  WHERE user_id = ?`,
		s.Timezone, s.ActiveRuntime, s.DeliveryChannel, s.ModelHints,
		s.BriefingTime, s.RestartTime, s.DistillationTime,
		s.TranscriptWindowN, s.RetrievalK, s.TgFormat, boolToInt(s.DistillNotify), s.UserID)
	return err
}
