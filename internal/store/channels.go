package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

// ChannelIdentity links an external sender to an app user. BotID
// distinguishes which Telegram bot the sender is paired to (0 = the legacy
// single-token bot, or a non-bot channel); the same Telegram account can pair to
// several bots, one identity row each.
type ChannelIdentity struct {
	UserID     int64
	Channel    string
	BotID      int64
	ExternalID string
	ChatID     string
}

// GetIdentityByExternal finds the app user paired to an external sender on a
// specific bot, or ErrNotFound when the sender is unknown (and must be silently
// dropped/paired).
func (db *DB) GetIdentityByExternal(ctx context.Context, channel string, botID int64, externalID string) (*ChannelIdentity, error) {
	var c ChannelIdentity
	err := db.QueryRowContext(ctx,
		`SELECT user_id, channel, bot_id, external_id, chat_id FROM channel_identities
		  WHERE channel = ? AND bot_id = ? AND external_id = ?`, channel, botID, externalID).
		Scan(&c.UserID, &c.Channel, &c.BotID, &c.ExternalID, &c.ChatID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// TelegramUserIsPaired reports whether a Telegram user id is paired to an account
// (i.e. has a DM identity, whose external_id is the user's positive Telegram id;
// group identities use the negative chat id and never match). Used to gate write
// operations in group chats to registered users.
func (db *DB) TelegramUserIsPaired(ctx context.Context, telegramID int64) (bool, error) {
	var exists int
	err := db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM channel_identities WHERE channel = 'telegram' AND external_id = ?)`,
		strconv.FormatInt(telegramID, 10)).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists == 1, nil
}

// GetIdentityByUserBot returns a user's identity on a specific bot (for delivery).
func (db *DB) GetIdentityByUserBot(ctx context.Context, userID int64, channel string, botID int64) (*ChannelIdentity, error) {
	var c ChannelIdentity
	err := db.QueryRowContext(ctx,
		`SELECT user_id, channel, bot_id, external_id, chat_id FROM channel_identities
		  WHERE user_id = ? AND channel = ? AND bot_id = ? ORDER BY id DESC LIMIT 1`, userID, channel, botID).
		Scan(&c.UserID, &c.Channel, &c.BotID, &c.ExternalID, &c.ChatID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetIdentityByUser returns any of a user's identities on a channel (most recent
// first). Used where the specific bot doesn't matter (e.g. "is the user paired
// to Telegram at all?").
func (db *DB) GetIdentityByUser(ctx context.Context, userID int64, channel string) (*ChannelIdentity, error) {
	var c ChannelIdentity
	err := db.QueryRowContext(ctx,
		`SELECT user_id, channel, bot_id, external_id, chat_id FROM channel_identities
		  WHERE user_id = ? AND channel = ? ORDER BY id DESC LIMIT 1`, userID, channel).
		Scan(&c.UserID, &c.Channel, &c.BotID, &c.ExternalID, &c.ChatID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ListIdentitiesByUser returns all of a user's identities on a channel (one per
// paired bot) — for showing per-bot pairing status in the portal.
func (db *DB) ListIdentitiesByUser(ctx context.Context, userID int64, channel string) ([]ChannelIdentity, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT user_id, channel, bot_id, external_id, chat_id FROM channel_identities
		  WHERE user_id = ? AND channel = ? ORDER BY bot_id`, userID, channel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChannelIdentity
	for rows.Next() {
		var c ChannelIdentity
		if err := rows.Scan(&c.UserID, &c.Channel, &c.BotID, &c.ExternalID, &c.ChatID); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateIdentity links an external sender to a user on a bot, replacing any prior
// link for that (channel, bot_id, external_id).
func (db *DB) CreateIdentity(ctx context.Context, c ChannelIdentity) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO channel_identities(user_id, channel, bot_id, external_id, chat_id)
		 VALUES(?,?,?,?,?)
		 ON CONFLICT(channel, bot_id, external_id) DO UPDATE SET user_id = excluded.user_id, chat_id = excluded.chat_id`,
		c.UserID, c.Channel, c.BotID, c.ExternalID, c.ChatID)
	return err
}

// DeleteIdentity removes a single paired chat (unpair) — scoped to one external
// sender on one bot, since a bot can have several chats paired. A no-op if no
// such identity exists.
func (db *DB) DeleteIdentity(ctx context.Context, userID int64, channel string, botID int64, externalID string) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM channel_identities WHERE user_id = ? AND channel = ? AND bot_id = ? AND external_id = ?`,
		userID, channel, botID, externalID)
	return err
}

// PairingCode is a short-lived code issued to an unknown sender, scoped to the
// bot they messaged.
type PairingCode struct {
	Code       string
	Channel    string
	BotID      int64
	ExternalID string
	ChatID     string
}

// UpsertPairingCode issues (or replaces) the pairing code for an external sender
// on a specific bot. expiresAt is an RFC3339/SQLite datetime string.
func (db *DB) UpsertPairingCode(ctx context.Context, code, channel string, botID int64, externalID, chatID, expiresAt string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// One active code per sender per bot: clear old ones first.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM pairing_codes WHERE channel = ? AND bot_id = ? AND external_id = ?`, channel, botID, externalID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO pairing_codes(code, channel, bot_id, external_id, chat_id, expires_at)
		 VALUES(?,?,?,?,?,?)`, code, channel, botID, externalID, chatID, expiresAt); err != nil {
		return err
	}
	return tx.Commit()
}

// ConsumePairingCode validates and deletes a pairing code, returning its sender
// details (including which bot it was issued for). Expired or unknown codes yield
// ErrNotFound.
func (db *DB) ConsumePairingCode(ctx context.Context, code string) (*PairingCode, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var p PairingCode
	err = tx.QueryRowContext(ctx,
		`SELECT code, channel, bot_id, external_id, chat_id FROM pairing_codes
		  WHERE code = ? AND expires_at > datetime('now')`, code).
		Scan(&p.Code, &p.Channel, &p.BotID, &p.ExternalID, &p.ChatID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pairing_codes WHERE code = ?`, code); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &p, nil
}
