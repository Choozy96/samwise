-- Per-user secrets: encrypted name=value pairs the user provides (API tokens,
-- etc.) that are injected as environment variables into their agent runs, so
-- skill scripts can read them without the secret ever touching memory, the
-- prompt, or the chat transcript. value_enc is AES-GCM (the MASTER_KEY box),
-- the same scheme as MCP server credentials.
CREATE TABLE user_secrets (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT    NOT NULL,            -- env var name, e.g. TODOIST_TOKEN
    value_enc  TEXT    NOT NULL,            -- encrypted value
    created_at TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT    NOT NULL DEFAULT (datetime('now')),
    UNIQUE(user_id, name)
);
