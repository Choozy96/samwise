package store

import (
	"context"
	"database/sql"
	"errors"
)

// SlackBot is a per-user Slack app, bound to an agent. BotTokenEnc (xoxb-…) and
// AppTokenEnc (xapp-…, Socket Mode) are AES-GCM blobs; the store never decrypts
// — the orchestrator/web layer holds the box. AgentID 0 means unbound (the
// user's active agent answers).
type SlackBot struct {
	ID          int64
	UserID      int64
	Label       string
	BotTokenEnc string
	AppTokenEnc string
	BotUserID   string // cached from auth.test (U…), for mention detection
	TeamName    string // cached workspace name, for display
	AgentID     int64  // 0 => unbound
	Enabled     bool
	CreatedAt   string
}

const slackBotSelect = `SELECT id, user_id, label, bot_token_enc, app_token_enc, bot_user_id, team_name, COALESCE(agent_id,0), enabled, created_at FROM slack_bots`

// CreateSlackBot inserts an app (caller encrypts both tokens) and returns its id.
func (db *DB) CreateSlackBot(ctx context.Context, b SlackBot) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO slack_bots(user_id, label, bot_token_enc, app_token_enc, bot_user_id, team_name, agent_id, enabled)
		 VALUES(?,?,?,?,?,?,?,?)`,
		b.UserID, b.Label, b.BotTokenEnc, b.AppTokenEnc, b.BotUserID, b.TeamName,
		nullableID(b.AgentID), boolToInt(b.Enabled))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetSlackBot loads a user-owned app by id.
func (db *DB) GetSlackBot(ctx context.Context, userID, id int64) (*SlackBot, error) {
	return scanSlackBot(db.QueryRowContext(ctx, slackBotSelect+` WHERE id = ? AND user_id = ?`, id, userID))
}

// ListSlackBots returns a user's apps, oldest first.
func (db *DB) ListSlackBots(ctx context.Context, userID int64) ([]SlackBot, error) {
	return db.querySlackBots(ctx, slackBotSelect+` WHERE user_id = ? ORDER BY id`, userID)
}

// ListEnabledSlackBots returns every enabled app across all users — the Slack
// Manager's source of truth for which Socket Mode connections to run.
func (db *DB) ListEnabledSlackBots(ctx context.Context) ([]SlackBot, error) {
	return db.querySlackBots(ctx, slackBotSelect+` WHERE enabled = 1 ORDER BY id`)
}

// UpdateSlackBot updates an app's label, bound agent, and enabled flag (not the
// tokens — replace those via UpdateSlackBotTokens).
func (db *DB) UpdateSlackBot(ctx context.Context, userID, id int64, label string, agentID int64, enabled bool) error {
	_, err := db.ExecContext(ctx,
		`UPDATE slack_bots SET label = ?, agent_id = ?, enabled = ? WHERE id = ? AND user_id = ?`,
		label, nullableID(agentID), boolToInt(enabled), id, userID)
	return err
}

// UpdateSlackBotTokens replaces an app's encrypted tokens (and clears the cached
// identity so the Manager re-resolves it).
func (db *DB) UpdateSlackBotTokens(ctx context.Context, userID, id int64, botTokenEnc, appTokenEnc string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE slack_bots SET bot_token_enc = ?, app_token_enc = ?, bot_user_id = '', team_name = ''
		  WHERE id = ? AND user_id = ?`, botTokenEnc, appTokenEnc, id, userID)
	return err
}

// SetSlackBotIdentity caches the app's bot user id + workspace name (auth.test).
func (db *DB) SetSlackBotIdentity(ctx context.Context, id int64, botUserID, teamName string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE slack_bots SET bot_user_id = ?, team_name = ? WHERE id = ?`, botUserID, teamName, id)
	return err
}

// DeleteSlackBot removes a user-owned app plus its identities and pairing codes
// (keyed by bot_id, not an FK — mirror of DeleteTelegramBot).
func (db *DB) DeleteSlackBot(ctx context.Context, userID, id int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM channel_identities WHERE channel = 'slack' AND bot_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM pairing_codes WHERE channel = 'slack' AND bot_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM slack_bots WHERE id = ? AND user_id = ?`, id, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// SlackBotAgentBinding returns the app (if any) owned by userID bound to
// agentID — used to route an agent's scheduled output to its own Slack app.
func (db *DB) SlackBotAgentBinding(ctx context.Context, userID, agentID int64) (*SlackBot, error) {
	return scanSlackBot(db.QueryRowContext(ctx,
		slackBotSelect+` WHERE user_id = ? AND agent_id = ? AND enabled = 1 ORDER BY id LIMIT 1`, userID, agentID))
}

func (db *DB) querySlackBots(ctx context.Context, query string, args ...any) ([]SlackBot, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SlackBot
	for rows.Next() {
		var b SlackBot
		var enabled int
		if err := rows.Scan(&b.ID, &b.UserID, &b.Label, &b.BotTokenEnc, &b.AppTokenEnc,
			&b.BotUserID, &b.TeamName, &b.AgentID, &enabled, &b.CreatedAt); err != nil {
			return nil, err
		}
		b.Enabled = enabled != 0
		out = append(out, b)
	}
	return out, rows.Err()
}

func scanSlackBot(row *sql.Row) (*SlackBot, error) {
	var b SlackBot
	var enabled int
	err := row.Scan(&b.ID, &b.UserID, &b.Label, &b.BotTokenEnc, &b.AppTokenEnc,
		&b.BotUserID, &b.TeamName, &b.AgentID, &enabled, &b.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	b.Enabled = enabled != 0
	return &b, nil
}
