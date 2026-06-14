package store

import (
	"context"
	"database/sql"
	"errors"
)

// TelegramBot is a per-user Telegram bot, bound to an agent. TokenEnc is the
// AES-GCM blob; the store never decrypts — the orchestrator/web layer holds the
// box. AgentID 0 means unbound (the user's active agent answers).
type TelegramBot struct {
	ID        int64
	UserID    int64
	Label     string
	TokenEnc  string
	Username  string
	AgentID   int64 // 0 => unbound
	Enabled   bool
	CreatedAt string
}

const tgBotSelect = `SELECT id, user_id, label, token_enc, username, COALESCE(agent_id,0), enabled, created_at FROM telegram_bots`

// CreateTelegramBot inserts a bot (caller encrypts the token) and returns its id.
func (db *DB) CreateTelegramBot(ctx context.Context, b TelegramBot) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO telegram_bots(user_id, label, token_enc, username, agent_id, enabled)
		 VALUES(?,?,?,?,?,?)`,
		b.UserID, b.Label, b.TokenEnc, b.Username, nullableID(b.AgentID), boolToInt(b.Enabled))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetTelegramBot loads a user-owned bot by id.
func (db *DB) GetTelegramBot(ctx context.Context, userID, id int64) (*TelegramBot, error) {
	return scanTelegramBot(db.QueryRowContext(ctx, tgBotSelect+` WHERE id = ? AND user_id = ?`, id, userID))
}

// ListTelegramBots returns a user's bots, oldest first.
func (db *DB) ListTelegramBots(ctx context.Context, userID int64) ([]TelegramBot, error) {
	return db.queryTelegramBots(ctx, tgBotSelect+` WHERE user_id = ? ORDER BY id`, userID)
}

// ListEnabledTelegramBots returns every enabled bot across all users — the
// Manager's source of truth for which pollers to run.
func (db *DB) ListEnabledTelegramBots(ctx context.Context) ([]TelegramBot, error) {
	return db.queryTelegramBots(ctx, tgBotSelect+` WHERE enabled = 1 ORDER BY id`)
}

// UpdateTelegramBot updates a bot's label, bound agent, and enabled flag (not the
// token — replace that via UpdateTelegramBotToken).
func (db *DB) UpdateTelegramBot(ctx context.Context, userID, id int64, label string, agentID int64, enabled bool) error {
	_, err := db.ExecContext(ctx,
		`UPDATE telegram_bots SET label = ?, agent_id = ?, enabled = ? WHERE id = ? AND user_id = ?`,
		label, nullableID(agentID), boolToInt(enabled), id, userID)
	return err
}

// UpdateTelegramBotToken replaces a bot's encrypted token (and clears the cached
// username so the Manager re-fetches it).
func (db *DB) UpdateTelegramBotToken(ctx context.Context, userID, id int64, tokenEnc string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE telegram_bots SET token_enc = ?, username = '' WHERE id = ? AND user_id = ?`, tokenEnc, id, userID)
	return err
}

// SetTelegramBotUsername caches the @username resolved from getMe.
func (db *DB) SetTelegramBotUsername(ctx context.Context, id int64, username string) error {
	_, err := db.ExecContext(ctx, `UPDATE telegram_bots SET username = ? WHERE id = ?`, username, id)
	return err
}

// DeleteTelegramBot removes a user-owned bot. Its channel_identities cascade only
// via app logic, so clear them too (they're keyed by bot_id, not an FK).
func (db *DB) DeleteTelegramBot(ctx context.Context, userID, id int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM channel_identities WHERE channel = 'telegram' AND bot_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM pairing_codes WHERE channel = 'telegram' AND bot_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM telegram_bots WHERE id = ? AND user_id = ?`, id, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// BotAgentBinding returns the bot (if any) owned by userID that is bound to
// agentID — used to route an agent's scheduled output to its own bot.
func (db *DB) BotAgentBinding(ctx context.Context, userID, agentID int64) (*TelegramBot, error) {
	return scanTelegramBot(db.QueryRowContext(ctx,
		tgBotSelect+` WHERE user_id = ? AND agent_id = ? AND enabled = 1 ORDER BY id LIMIT 1`, userID, agentID))
}

func (db *DB) queryTelegramBots(ctx context.Context, query string, args ...any) ([]TelegramBot, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TelegramBot
	for rows.Next() {
		b, err := scanTelegramBotRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func scanTelegramBot(row *sql.Row) (*TelegramBot, error) {
	var b TelegramBot
	var enabled int
	err := row.Scan(&b.ID, &b.UserID, &b.Label, &b.TokenEnc, &b.Username, &b.AgentID, &enabled, &b.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	b.Enabled = enabled != 0
	return &b, nil
}

func scanTelegramBotRows(rows *sql.Rows) (TelegramBot, error) {
	var b TelegramBot
	var enabled int
	err := rows.Scan(&b.ID, &b.UserID, &b.Label, &b.TokenEnc, &b.Username, &b.AgentID, &enabled, &b.CreatedAt)
	b.Enabled = enabled != 0
	return b, err
}

// nullableID maps 0 to SQL NULL (for the optional agent_id FK), else the id.
func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
