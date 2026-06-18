package store

import (
	"context"
	"database/sql"
	"errors"
)

// Agent is a named persona (multi-agent setup). Soul is the agent's system
// prompt; empty falls back to the base assistant prompt. Model/Runtime are
// optional per-agent overrides.
type Agent struct {
	ID          int64
	UserID      int64
	Name        string
	Description string
	Soul        string
	Model       string
	Runtime     string
	IsDefault   bool
	Enabled     bool
	CreatedAt   string
	UpdatedAt   string
}

const agentSelect = `SELECT id, user_id, name, description, soul, model, runtime, is_default, enabled, created_at, updated_at FROM agents`

// CreateAgent inserts an agent and returns its id.
func (db *DB) CreateAgent(ctx context.Context, a Agent) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO agents(user_id, name, description, soul, model, runtime, is_default, enabled)
		 VALUES(?,?,?,?,?,?,?,?)`,
		a.UserID, a.Name, a.Description, a.Soul, a.Model, a.Runtime,
		boolToInt(a.IsDefault), boolToInt(a.Enabled))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetAgent loads a user-owned agent by id.
func (db *DB) GetAgent(ctx context.Context, userID, id int64) (*Agent, error) {
	return scanAgent(db.QueryRowContext(ctx, agentSelect+` WHERE id = ? AND user_id = ?`, id, userID))
}

// GetAgentByName loads a user's agent by (case-insensitive) name.
func (db *DB) GetAgentByName(ctx context.Context, userID int64, name string) (*Agent, error) {
	return scanAgent(db.QueryRowContext(ctx,
		agentSelect+` WHERE user_id = ? AND name = ? COLLATE NOCASE LIMIT 1`, userID, name))
}

// ListAgents returns a user's agents (default first, then by name).
func (db *DB) ListAgents(ctx context.Context, userID int64) ([]Agent, error) {
	rows, err := db.QueryContext(ctx,
		agentSelect+` WHERE user_id = ? ORDER BY is_default DESC, name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Agent
	for rows.Next() {
		a, err := scanAgentRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetDefaultAgent returns the user's default agent.
func (db *DB) GetDefaultAgent(ctx context.Context, userID int64) (*Agent, error) {
	return scanAgent(db.QueryRowContext(ctx,
		agentSelect+` WHERE user_id = ? AND is_default = 1 LIMIT 1`, userID))
}

// GetActiveAgent returns the user's active agent (per user_settings), falling
// back to the default agent if unset or missing.
func (db *DB) GetActiveAgent(ctx context.Context, userID int64) (*Agent, error) {
	var activeID int64
	_ = db.QueryRowContext(ctx, `SELECT active_agent_id FROM user_settings WHERE user_id = ?`, userID).Scan(&activeID)
	if activeID != 0 {
		if a, err := db.GetAgent(ctx, userID, activeID); err == nil {
			return a, nil
		}
	}
	return db.GetDefaultAgent(ctx, userID)
}

// SetActiveAgent records the user's active agent.
func (db *DB) SetActiveAgent(ctx context.Context, userID, agentID int64) error {
	_, err := db.ExecContext(ctx, `UPDATE user_settings SET active_agent_id = ? WHERE user_id = ?`, agentID, userID)
	return err
}

// UpdateAgent updates an owned agent's editable fields.
func (db *DB) UpdateAgent(ctx context.Context, a Agent) error {
	_, err := db.ExecContext(ctx,
		`UPDATE agents SET name=?, description=?, soul=?, model=?, runtime=?, enabled=?, updated_at=datetime('now')
		  WHERE id=? AND user_id=?`,
		a.Name, a.Description, a.Soul, a.Model, a.Runtime, boolToInt(a.Enabled), a.ID, a.UserID)
	return err
}

// SetDefaultAgent makes one agent the user's default (clearing the others) in a
// single transaction.
func (db *DB) SetDefaultAgent(ctx context.Context, userID, id int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE agents SET is_default = 0 WHERE user_id = ?`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agents SET is_default = 1 WHERE id = ? AND user_id = ?`, id, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteAgent removes a non-default agent the user owns. Deleting the default is
// rejected (callers should reassign default first).
func (db *DB) DeleteAgent(ctx context.Context, userID, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM agents WHERE id=? AND user_id=? AND is_default=0`, id, userID)
	return err
}

// EnsureDefaultAgent creates a default agent for a user if none exists, returning
// the default agent's id. Used when provisioning a new account.
func (db *DB) EnsureDefaultAgent(ctx context.Context, userID int64) (int64, error) {
	if a, err := db.GetDefaultAgent(ctx, userID); err == nil {
		return a.ID, nil
	}
	id, err := db.CreateAgent(ctx, Agent{
		UserID:      userID,
		Name:        "Assistant",
		Description: "Your general-purpose assistant.",
		IsDefault:   true,
		Enabled:     true,
	})
	if err != nil {
		return 0, err
	}
	_ = db.SetActiveAgent(ctx, userID, id)
	return id, nil
}

func scanAgent(row *sql.Row) (*Agent, error) {
	var a Agent
	var isDefault, enabled int
	err := row.Scan(&a.ID, &a.UserID, &a.Name, &a.Description, &a.Soul, &a.Model, &a.Runtime,
		&isDefault, &enabled, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.IsDefault = isDefault != 0
	a.Enabled = enabled != 0
	return &a, nil
}

func scanAgentRows(rows *sql.Rows) (Agent, error) {
	var a Agent
	var isDefault, enabled int
	err := rows.Scan(&a.ID, &a.UserID, &a.Name, &a.Description, &a.Soul, &a.Model, &a.Runtime,
		&isDefault, &enabled, &a.CreatedAt, &a.UpdatedAt)
	a.IsDefault = isDefault != 0
	a.Enabled = enabled != 0
	return a, err
}
