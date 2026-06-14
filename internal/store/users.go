package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CountUsers returns the number of accounts. Used to decide whether a new
// account is the first (and therefore admin).
func (db *DB) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser inserts a user and its default settings row in one transaction.
// The first account created (CountUsers == 0) should be passed isAdmin=true by
// the caller; this method does not decide admin-ness itself.
func (db *DB) CreateUser(ctx context.Context, username, passwordHash string, isAdmin bool) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO users(username, password_hash, is_admin) VALUES(?,?,?)`,
		username, passwordHash, boolToInt(isAdmin))
	if err != nil {
		return 0, fmt.Errorf("inserting user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO user_settings(user_id) VALUES(?)`, id); err != nil {
		return 0, fmt.Errorf("inserting settings: %w", err)
	}
	// Seed a default agent and make it active (multi-agent setup).
	ares, err := tx.ExecContext(ctx,
		`INSERT INTO agents(user_id, name, description, is_default, enabled)
		 VALUES(?, 'Assistant', 'Your general-purpose assistant.', 1, 1)`, id)
	if err != nil {
		return 0, fmt.Errorf("inserting default agent: %w", err)
	}
	agentID, err := ares.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE user_settings SET active_agent_id = ? WHERE user_id = ?`, agentID, id); err != nil {
		return 0, fmt.Errorf("setting active agent: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// GetUserByUsername returns the user with the given username, or ErrNotFound.
func (db *DB) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	return db.scanUser(db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, is_admin, disabled, created_at
		   FROM users WHERE username = ?`, username))
}

// GetUserByID returns the user with the given id, or ErrNotFound.
func (db *DB) GetUserByID(ctx context.Context, id int64) (*User, error) {
	return db.scanUser(db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, is_admin, disabled, created_at
		   FROM users WHERE id = ?`, id))
}

// ListUsers returns all users ordered by id (admin view).
func (db *DB) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, username, password_hash, is_admin, disabled, created_at
		   FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var admin, disabled int
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &admin, &disabled, &u.CreatedAt); err != nil {
			return nil, err
		}
		u.IsAdmin = admin != 0
		u.Disabled = disabled != 0
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdatePassword replaces a user's password hash. The caller hashes the new
// password (argon2id); this only persists it.
func (db *DB) UpdatePassword(ctx context.Context, id int64, passwordHash string) error {
	res, err := db.ExecContext(ctx,
		`UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetUserDisabled enables or disables an account (admin action, spec §9).
func (db *DB) SetUserDisabled(ctx context.Context, id int64, disabled bool) error {
	_, err := db.ExecContext(ctx, `UPDATE users SET disabled = ? WHERE id = ?`,
		boolToInt(disabled), id)
	return err
}

func (db *DB) scanUser(row *sql.Row) (*User, error) {
	var u User
	var admin, disabled int
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &admin, &disabled, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.IsAdmin = admin != 0
	u.Disabled = disabled != 0
	return &u, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
