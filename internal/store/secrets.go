package store

import "context"

// Secret is a per-user encrypted name=value pair. ValueEnc is the AES-GCM blob;
// the store never decrypts — the orchestrator/web layer holds the box. Kind is
// 'value' (plain token) or 'oauth' (token JSON the app parses read-only for expiry).
type Secret struct {
	ID        int64
	Name      string
	ValueEnc  string
	Kind      string
	CreatedAt string
}

// SetSecret inserts or updates (by name) a user's secret. The caller encrypts
// valueEnc before calling.
func (db *DB) SetSecret(ctx context.Context, userID int64, name, valueEnc, kind string) error {
	if kind == "" {
		kind = "value"
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO user_secrets(user_id, name, value_enc, kind) VALUES(?,?,?,?)
		 ON CONFLICT(user_id, name) DO UPDATE SET value_enc = excluded.value_enc,
		     kind = excluded.kind, updated_at = datetime('now')`,
		userID, name, valueEnc, kind)
	return err
}

// ListSecrets returns a user's secrets (name + encrypted value + kind), ordered
// by name. The web layer shows only names; the orchestrator decrypts values for runs.
func (db *DB) ListSecrets(ctx context.Context, userID int64) ([]Secret, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, value_enc, kind, created_at FROM user_secrets
		  WHERE user_id = ? ORDER BY name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Secret
	for rows.Next() {
		var s Secret
		if err := rows.Scan(&s.ID, &s.Name, &s.ValueEnc, &s.Kind, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteSecret removes a user's secret by name.
func (db *DB) DeleteSecret(ctx context.Context, userID int64, name string) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM user_secrets WHERE user_id = ? AND name = ?`, userID, name)
	return err
}
