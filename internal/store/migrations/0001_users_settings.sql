-- Users and per-user settings. The first account created becomes
-- admin. Passwords are argon2id hashes. Every user-owned row in
-- later migrations references users(id).

CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT    NOT NULL UNIQUE,
    password_hash TEXT    NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    disabled      INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT    NOT NULL DEFAULT (datetime('now'))
);

-- One settings row per user. Created alongside the
-- user. Schedule times are local HH:MM strings interpreted in the user's
-- timezone; tz_mode on individual jobs governs drift.
CREATE TABLE user_settings (
    user_id             INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    timezone            TEXT    NOT NULL DEFAULT 'UTC',            -- IANA name
    active_runtime      TEXT    NOT NULL DEFAULT 'claude-headless',-- claude-headless|claude-channels|codex-exec
    delivery_channel    TEXT    NOT NULL DEFAULT 'web',            -- web|telegram (default telegram once paired)
    model_hints         TEXT    NOT NULL DEFAULT '{}',            -- JSON: per-job-type model overrides
    briefing_time       TEXT    NOT NULL DEFAULT '07:00',
    restart_time        TEXT    NOT NULL DEFAULT '04:00',
    distillation_time   TEXT    NOT NULL DEFAULT '23:30',
    transcript_window_n INTEGER NOT NULL DEFAULT 20,              -- last N messages into context
    retrieval_k         INTEGER NOT NULL DEFAULT 8                -- top-K memory rows into context
);
