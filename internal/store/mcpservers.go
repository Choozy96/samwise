package store

import (
	"context"
	"database/sql"
	"errors"
)

// MCPServer is a registered external MCP server. UserID == 0 means a
// global (admin-managed) entry visible to all users. SecretEnc is the encrypted
// {env,headers} blob (decrypted only at dispatch by the orchestrator).
type MCPServer struct {
	ID        int64
	UserID    int64 // 0 = global
	Name      string
	Transport string // stdio | http
	Command   string
	ArgsJSON  string
	URL       string
	SecretEnc string
	ToolsJSON string
	Enabled   bool
	CreatedAt string
}

// IsGlobal reports whether this is an admin-managed global entry.
func (m MCPServer) IsGlobal() bool { return m.UserID == 0 }

const mcpSelect = `SELECT id, COALESCE(user_id,0), name, transport, command, args_json, url,
	secret_enc, tools_json, enabled, created_at FROM mcp_servers`

// CreateMCPServer inserts a registry entry and returns its id.
func (db *DB) CreateMCPServer(ctx context.Context, m MCPServer) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO mcp_servers(user_id, name, transport, command, args_json, url, secret_enc, tools_json, enabled)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		nullInt(m.UserID), m.Name, m.Transport, m.Command, m.ArgsJSON, m.URL,
		m.SecretEnc, orEmptyJSON(m.ToolsJSON), boolToInt(m.Enabled))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetMCPServer returns a registry entry by id (caller authorizes ownership).
func (db *DB) GetMCPServer(ctx context.Context, id int64) (*MCPServer, error) {
	return scanMCP(db.QueryRowContext(ctx, mcpSelect+` WHERE id = ?`, id))
}

// ListMCPServersForUser returns a user's own entries plus globals, for the UI.
func (db *DB) ListMCPServersForUser(ctx context.Context, userID int64) ([]MCPServer, error) {
	return db.queryMCP(ctx, mcpSelect+
		` WHERE user_id = ? OR user_id IS NULL ORDER BY user_id IS NULL DESC, name`, userID)
}

// ListEnabledMCPServers returns the enabled entries (own + global) to compose
// into a run.
func (db *DB) ListEnabledMCPServers(ctx context.Context, userID int64) ([]MCPServer, error) {
	return db.queryMCP(ctx, mcpSelect+
		` WHERE (user_id = ? OR user_id IS NULL) AND enabled = 1 ORDER BY name`, userID)
}

// UpdateMCPServer updates a user-owned entry's editable fields. secret_enc is
// only overwritten when newSecret is non-empty (so edits without re-entering
// credentials keep the existing ones).
func (db *DB) UpdateMCPServer(ctx context.Context, m MCPServer, newSecret bool) error {
	if newSecret {
		_, err := db.ExecContext(ctx,
			`UPDATE mcp_servers SET name=?, transport=?, command=?, args_json=?, url=?, secret_enc=?, enabled=?
			  WHERE id=? AND user_id IS ?`,
			m.Name, m.Transport, m.Command, m.ArgsJSON, m.URL, m.SecretEnc, boolToInt(m.Enabled),
			m.ID, nullInt(m.UserID))
		return err
	}
	_, err := db.ExecContext(ctx,
		`UPDATE mcp_servers SET name=?, transport=?, command=?, args_json=?, url=?, enabled=?
		  WHERE id=? AND user_id IS ?`,
		m.Name, m.Transport, m.Command, m.ArgsJSON, m.URL, boolToInt(m.Enabled),
		m.ID, nullInt(m.UserID))
	return err
}

// SetMCPServerTools records the discovered tool names (JSON array).
func (db *DB) SetMCPServerTools(ctx context.Context, id int64, toolsJSON string) error {
	_, err := db.ExecContext(ctx, `UPDATE mcp_servers SET tools_json=? WHERE id=?`, toolsJSON, id)
	return err
}

// SetMCPServerEnabled toggles a user-owned entry.
func (db *DB) SetMCPServerEnabled(ctx context.Context, userID, id int64, enabled bool) error {
	_, err := db.ExecContext(ctx,
		`UPDATE mcp_servers SET enabled=? WHERE id=? AND user_id IS ?`,
		boolToInt(enabled), id, nullInt(userID))
	return err
}

// DeleteMCPServer removes a user-owned (or, for admin, global) entry.
func (db *DB) DeleteMCPServer(ctx context.Context, userID, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM mcp_servers WHERE id=? AND user_id IS ?`, id, nullInt(userID))
	return err
}

func (db *DB) queryMCP(ctx context.Context, query string, args ...any) ([]MCPServer, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MCPServer
	for rows.Next() {
		m, err := scanMCPRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func scanMCP(row *sql.Row) (*MCPServer, error) {
	var m MCPServer
	var enabled int
	err := row.Scan(&m.ID, &m.UserID, &m.Name, &m.Transport, &m.Command, &m.ArgsJSON, &m.URL,
		&m.SecretEnc, &m.ToolsJSON, &enabled, &m.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	m.Enabled = enabled != 0
	return &m, nil
}

func scanMCPRows(rows *sql.Rows) (MCPServer, error) {
	var m MCPServer
	var enabled int
	err := rows.Scan(&m.ID, &m.UserID, &m.Name, &m.Transport, &m.Command, &m.ArgsJSON, &m.URL,
		&m.SecretEnc, &m.ToolsJSON, &enabled, &m.CreatedAt)
	m.Enabled = enabled != 0
	return m, err
}

// nullInt returns nil for 0 (global) so the column stores SQL NULL.
func nullInt(i int64) any {
	if i == 0 {
		return nil
	}
	return i
}

func orEmptyJSON(s string) string {
	if s == "" {
		return "[]"
	}
	return s
}
