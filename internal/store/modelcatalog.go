package store

import "context"

// Model is one entry in the admin-configurable model catalog (migration 0028).
type Model struct {
	ID      int64
	Alias   string // short name for /model + form values; may be "" for id-only rows
	ModelID string // id passed to the runtime; "" = the runtime's own default
	Label   string
	Sort    int
	Enabled bool
}

// ListModels returns the catalog ordered by sort then label. enabledOnly hides
// disabled rows (for the user-facing pickers); admin passes false to see all.
func (db *DB) ListModels(ctx context.Context, enabledOnly bool) ([]Model, error) {
	q := `SELECT id, alias, model_id, label, sort, enabled FROM models`
	if enabledOnly {
		q += ` WHERE enabled = 1`
	}
	q += ` ORDER BY sort, label`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Model
	for rows.Next() {
		var m Model
		if err := rows.Scan(&m.ID, &m.Alias, &m.ModelID, &m.Label, &m.Sort, &m.Enabled); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetModel fetches one catalog row by id.
func (db *DB) GetModel(ctx context.Context, id int64) (*Model, error) {
	var m Model
	err := db.QueryRowContext(ctx,
		`SELECT id, alias, model_id, label, sort, enabled FROM models WHERE id = ?`, id).
		Scan(&m.ID, &m.Alias, &m.ModelID, &m.Label, &m.Sort, &m.Enabled)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// CreateModel adds a catalog entry, returning its id.
func (db *DB) CreateModel(ctx context.Context, m Model) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO models (alias, model_id, label, sort, enabled) VALUES (?, ?, ?, ?, ?)`,
		m.Alias, m.ModelID, m.Label, m.Sort, boolToInt(m.Enabled))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateModel updates a catalog entry by id.
func (db *DB) UpdateModel(ctx context.Context, m Model) error {
	_, err := db.ExecContext(ctx,
		`UPDATE models SET alias = ?, model_id = ?, label = ?, sort = ?, enabled = ? WHERE id = ?`,
		m.Alias, m.ModelID, m.Label, m.Sort, boolToInt(m.Enabled), m.ID)
	return err
}

// DeleteModel removes a catalog entry by id.
func (db *DB) DeleteModel(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM models WHERE id = ?`, id)
	return err
}
