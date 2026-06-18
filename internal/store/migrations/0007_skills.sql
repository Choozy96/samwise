-- Per-user (or global) skills: markdown instruction files that
-- shape the assistant's behavior. Enabled skills are surfaced to the runtime;
-- always_on skills are injected into every run's context, others are referenced
-- by name (e.g. by a scheduled job). Mirrored to the workspace for native
-- discovery when the channels runtime lands.

CREATE TABLE skills (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER REFERENCES users(id) ON DELETE CASCADE, -- NULL = global
    name        TEXT    NOT NULL,                 -- slug-ish; used in /skills/<name> paths
    description TEXT    NOT NULL DEFAULT '',
    content     TEXT    NOT NULL DEFAULT '',
    always_on   INTEGER NOT NULL DEFAULT 0,
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_skills_user ON skills(user_id, enabled);
