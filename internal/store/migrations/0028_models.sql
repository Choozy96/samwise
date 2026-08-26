-- Admin-configurable model catalog. Seeded with the current Claude lineup so
-- behavior is sensible out of the box; admins can then add/edit/remove models
-- (e.g. a new Claude release) from the Admin page with no code change.
--
-- Aliases carry the version (opus48, fable5) so /model is unambiguous across
-- releases. There is deliberately NO "default" row: "use the runtime's default"
-- is a hardcoded picker option, not a catalog entry an admin could break.
CREATE TABLE IF NOT EXISTS models (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    alias    TEXT    NOT NULL DEFAULT '',   -- short name for /model and form values ('' allowed for id-only rows)
    model_id TEXT    NOT NULL DEFAULT '',   -- id passed to the runtime
    label    TEXT    NOT NULL,              -- human label for the UI
    sort     INTEGER NOT NULL DEFAULT 0,    -- display order
    enabled  INTEGER NOT NULL DEFAULT 1
);

-- Aliases must be unique when set (so /model <alias> is unambiguous); id-only
-- rows may leave it blank.
CREATE UNIQUE INDEX IF NOT EXISTS idx_models_alias ON models(alias) WHERE alias != '';

-- OR REPLACE: seeds are the official entries — if an alias already exists (the
-- admin added the model themselves before updating, e.g. hand-adding "opus6"
-- ahead of the release that ships it), the seed REPLACES that row with the
-- official id/label instead of failing the migration or being skipped. Admin
-- rows with non-colliding aliases are never touched. Future seed migrations
-- must follow the same convention.
INSERT OR REPLACE INTO models (alias, model_id, label, sort, enabled) VALUES
    ('opus48',  'claude-opus-4-8',           'Claude Opus 4.8',  10, 1),
    ('sonnet5', 'claude-sonnet-5',           'Claude Sonnet 5',  20, 1),
    ('fable5',  'claude-fable-5',            'Claude Fable 5',   30, 1),
    ('haiku45', 'claude-haiku-4-5-20251001', 'Claude Haiku 4.5', 40, 1);
