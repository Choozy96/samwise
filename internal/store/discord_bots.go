package store

import (
	"context"
	"database/sql"
	"errors"
)

// DiscordBot is a per-user Discord bot, bound to an agent. TokenEnc is the
// AES-GCM blob (single token — the Gateway uses the same one); the store never
// decrypts. AgentID 0 means unbound (the user's active agent answers).
type DiscordBot struct {
	ID        int64
	UserID    int64
	Label     string
	TokenEnc  string
	BotUserID string // cached bot user id, for mention detection
	Username  string // cached bot username, for display
	AgentID   int64  // 0 => unbound
	Enabled   bool
	CreatedAt string
}

const discordBotSelect = `SELECT id, user_id, label, token_enc, bot_user_id, username, COALESCE(agent_id,0), enabled, created_at FROM discord_bots`

// CreateDiscordBot inserts a bot (caller encrypts the token) and returns its id.
func (db *DB) CreateDiscordBot(ctx context.Context, b DiscordBot) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO discord_bots(user_id, label, token_enc, bot_user_id, username, agent_id, enabled)
		 VALUES(?,?,?,?,?,?,?)`,
		b.UserID, b.Label, b.TokenEnc, b.BotUserID, b.Username, nullableID(b.AgentID), boolToInt(b.Enabled))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetDiscordBot loads a user-owned bot by id.
func (db *DB) GetDiscordBot(ctx context.Context, userID, id int64) (*DiscordBot, error) {
	return scanDiscordBot(db.QueryRowContext(ctx, discordBotSelect+` WHERE id = ? AND user_id = ?`, id, userID))
}

// ListDiscordBots returns a user's bots, oldest first.
func (db *DB) ListDiscordBots(ctx context.Context, userID int64) ([]DiscordBot, error) {
	return db.queryDiscordBots(ctx, discordBotSelect+` WHERE user_id = ? ORDER BY id`, userID)
}

// ListEnabledDiscordBots returns every enabled bot across all users — the
// Discord Manager's source of truth for which Gateway sessions to run.
func (db *DB) ListEnabledDiscordBots(ctx context.Context) ([]DiscordBot, error) {
	return db.queryDiscordBots(ctx, discordBotSelect+` WHERE enabled = 1 ORDER BY id`)
}

// UpdateDiscordBot updates a bot's label, bound agent, and enabled flag (not
// the token — replace that via UpdateDiscordBotToken).
func (db *DB) UpdateDiscordBot(ctx context.Context, userID, id int64, label string, agentID int64, enabled bool) error {
	_, err := db.ExecContext(ctx,
		`UPDATE discord_bots SET label = ?, agent_id = ?, enabled = ? WHERE id = ? AND user_id = ?`,
		label, nullableID(agentID), boolToInt(enabled), id, userID)
	return err
}

// UpdateDiscordBotToken replaces a bot's encrypted token (and clears the cached
// identity so the Manager re-resolves it).
func (db *DB) UpdateDiscordBotToken(ctx context.Context, userID, id int64, tokenEnc string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE discord_bots SET token_enc = ?, bot_user_id = '', username = '' WHERE id = ? AND user_id = ?`,
		tokenEnc, id, userID)
	return err
}

// SetDiscordBotIdentity caches the bot's user id + username.
func (db *DB) SetDiscordBotIdentity(ctx context.Context, id int64, botUserID, username string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE discord_bots SET bot_user_id = ?, username = ? WHERE id = ?`, botUserID, username, id)
	return err
}

// DeleteDiscordBot removes a user-owned bot plus its identities and pairing
// codes (keyed by bot_id, not an FK — mirror of DeleteSlackBot).
func (db *DB) DeleteDiscordBot(ctx context.Context, userID, id int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM channel_identities WHERE channel = 'discord' AND bot_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM pairing_codes WHERE channel = 'discord' AND bot_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM discord_bots WHERE id = ? AND user_id = ?`, id, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// DiscordBotAgentBinding returns the bot (if any) owned by userID bound to
// agentID — used to route an agent's scheduled output to its own Discord bot.
func (db *DB) DiscordBotAgentBinding(ctx context.Context, userID, agentID int64) (*DiscordBot, error) {
	return scanDiscordBot(db.QueryRowContext(ctx,
		discordBotSelect+` WHERE user_id = ? AND agent_id = ? AND enabled = 1 ORDER BY id LIMIT 1`, userID, agentID))
}

func (db *DB) queryDiscordBots(ctx context.Context, query string, args ...any) ([]DiscordBot, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DiscordBot
	for rows.Next() {
		var b DiscordBot
		var enabled int
		if err := rows.Scan(&b.ID, &b.UserID, &b.Label, &b.TokenEnc, &b.BotUserID,
			&b.Username, &b.AgentID, &enabled, &b.CreatedAt); err != nil {
			return nil, err
		}
		b.Enabled = enabled != 0
		out = append(out, b)
	}
	return out, rows.Err()
}

func scanDiscordBot(row *sql.Row) (*DiscordBot, error) {
	var b DiscordBot
	var enabled int
	err := row.Scan(&b.ID, &b.UserID, &b.Label, &b.TokenEnc, &b.BotUserID,
		&b.Username, &b.AgentID, &enabled, &b.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	b.Enabled = enabled != 0
	return &b, nil
}
