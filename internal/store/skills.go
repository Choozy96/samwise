package store

import (
	"context"
	"database/sql"
	"errors"
)

// Skill is a markdown instruction file. UserID == 0 means global.
// HasBundle marks a skill with files (scripts/assets) on disk from a ZIP import.
type Skill struct {
	ID          int64
	UserID      int64 // 0 = global
	Name        string
	Description string
	Content     string
	AlwaysOn    bool
	Enabled     bool
	HasBundle   bool
	CreatedAt   string
	UpdatedAt   string
}

// IsGlobal reports whether this is a global (admin-managed) skill.
func (s Skill) IsGlobal() bool { return s.UserID == 0 }

const skillSelect = `SELECT id, COALESCE(user_id,0), name, description, content, always_on, enabled, has_bundle, created_at, updated_at FROM skills`

// CreateSkill inserts a skill and returns its id.
func (db *DB) CreateSkill(ctx context.Context, s Skill) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO skills(user_id, name, description, content, always_on, enabled, has_bundle)
		 VALUES(?,?,?,?,?,?,?)`,
		nullInt(s.UserID), s.Name, s.Description, s.Content,
		boolToInt(s.AlwaysOn), boolToInt(s.Enabled), boolToInt(s.HasBundle))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpsertSkillByName creates or replaces a user's skill of the same name, used by
// ZIP import (re-importing updates in place). Returns the skill id.
func (db *DB) UpsertSkillByName(ctx context.Context, s Skill) (int64, error) {
	if existing, err := db.GetSkillByName(ctx, s.UserID, s.Name); err == nil && existing != nil && existing.UserID == s.UserID {
		s.ID = existing.ID
		s.AlwaysOn = existing.AlwaysOn
		_, err := db.ExecContext(ctx,
			`UPDATE skills SET description=?, content=?, has_bundle=?, enabled=1, updated_at=datetime('now')
			  WHERE id=? AND user_id=?`,
			s.Description, s.Content, boolToInt(s.HasBundle), s.ID, s.UserID)
		return s.ID, err
	}
	return db.CreateSkill(ctx, s)
}

// GetSkill returns a skill by id.
func (db *DB) GetSkill(ctx context.Context, id int64) (*Skill, error) {
	return scanSkill(db.QueryRowContext(ctx, skillSelect+` WHERE id = ?`, id))
}

// GetSkillByName returns a user's own skill by name, falling back to a global of
// the same name. Used to look up a skill a job references.
func (db *DB) GetSkillByName(ctx context.Context, userID int64, name string) (*Skill, error) {
	s, err := scanSkill(db.QueryRowContext(ctx,
		skillSelect+` WHERE name = ? AND (user_id = ? OR user_id IS NULL)
		             ORDER BY user_id IS NULL ASC LIMIT 1`, name, userID))
	return s, err
}

// ListSkillsForUser returns a user's own skills plus globals (globals last).
func (db *DB) ListSkillsForUser(ctx context.Context, userID int64) ([]Skill, error) {
	return db.querySkills(ctx,
		skillSelect+` WHERE user_id = ? OR user_id IS NULL ORDER BY user_id IS NULL DESC, name`, userID)
}

// ListEnabledSkills returns enabled skills (own + global) for context injection.
func (db *DB) ListEnabledSkills(ctx context.Context, userID int64) ([]Skill, error) {
	return db.querySkills(ctx,
		skillSelect+` WHERE (user_id = ? OR user_id IS NULL) AND enabled = 1 ORDER BY name`, userID)
}

// UpdateSkill updates an owned skill's editable fields.
func (db *DB) UpdateSkill(ctx context.Context, s Skill) error {
	_, err := db.ExecContext(ctx,
		`UPDATE skills SET name=?, description=?, content=?, always_on=?, enabled=?, updated_at=datetime('now')
		  WHERE id=? AND user_id IS ?`,
		s.Name, s.Description, s.Content, boolToInt(s.AlwaysOn), boolToInt(s.Enabled), s.ID, nullInt(s.UserID))
	return err
}

// SetSkillEnabled toggles an owned skill.
func (db *DB) SetSkillEnabled(ctx context.Context, userID, id int64, enabled bool) error {
	_, err := db.ExecContext(ctx,
		`UPDATE skills SET enabled=?, updated_at=datetime('now') WHERE id=? AND user_id IS ?`,
		boolToInt(enabled), id, nullInt(userID))
	return err
}

// DeleteSkill removes an owned skill.
func (db *DB) DeleteSkill(ctx context.Context, userID, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM skills WHERE id=? AND user_id IS ?`, id, nullInt(userID))
	return err
}

func (db *DB) querySkills(ctx context.Context, query string, args ...any) ([]Skill, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Skill
	for rows.Next() {
		s, err := scanSkillRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func scanSkill(row *sql.Row) (*Skill, error) {
	var s Skill
	var always, enabled, bundle int
	err := row.Scan(&s.ID, &s.UserID, &s.Name, &s.Description, &s.Content, &always, &enabled, &bundle, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.AlwaysOn = always != 0
	s.Enabled = enabled != 0
	s.HasBundle = bundle != 0
	return &s, nil
}

func scanSkillRows(rows *sql.Rows) (Skill, error) {
	var s Skill
	var always, enabled, bundle int
	err := rows.Scan(&s.ID, &s.UserID, &s.Name, &s.Description, &s.Content, &always, &enabled, &bundle, &s.CreatedAt, &s.UpdatedAt)
	s.AlwaysOn = always != 0
	s.Enabled = enabled != 0
	s.HasBundle = bundle != 0
	return s, err
}
