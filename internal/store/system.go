package store

import "context"

// System-setting keys.
const (
	// AnchorChatKey holds the admin-designated announcement chat as
	// "tg:<botID>:<chatID>" — the default destination for scheduled results
	// (unless a job or user configures otherwise) and system broadcasts.
	AnchorChatKey = "anchor_chat"
)

// GetSystemSetting returns a system setting's value, "" if unset.
func (db *DB) GetSystemSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := db.QueryRowContext(ctx, `SELECT value FROM system_settings WHERE key = ?`, key).Scan(&v)
	if err != nil {
		return "", nil // unset (or table momentarily unavailable) reads as empty
	}
	return v, nil
}

// SetSystemSetting upserts a system setting. An empty value clears it.
func (db *DB) SetSystemSetting(ctx context.Context, key, value string) error {
	if value == "" {
		_, err := db.ExecContext(ctx, `DELETE FROM system_settings WHERE key = ?`, key)
		return err
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO system_settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
