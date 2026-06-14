-- Working memory (spec §6): conversations, their transcripts, and a record of
-- each agent run. The rolling summary lives on the conversation row; the
-- transcript is the messages table. Tool-call summaries are stored as messages
-- with role='tool' so a rehydrated runtime knows which side effects already
-- happened (spec §5.5).

CREATE TABLE conversations (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id            INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel            TEXT    NOT NULL,            -- web | telegram
    harness_session_id TEXT,                        -- native-continuity optimization (spec §5.4)
    summary            TEXT    NOT NULL DEFAULT '', -- rolling summary
    summary_msg_count  INTEGER NOT NULL DEFAULT 0,  -- messages folded into summary so far
    created_at         TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at         TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_conversations_user ON conversations(user_id, channel);

CREATE TABLE messages (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role            TEXT    NOT NULL,               -- user | assistant | tool
    content         TEXT    NOT NULL,
    created_at      TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_messages_conversation ON messages(conversation_id, id);

CREATE TABLE runs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    conversation_id INTEGER REFERENCES conversations(id) ON DELETE SET NULL,
    runtime         TEXT    NOT NULL,               -- claude-headless | claude-channels | codex-exec
    model           TEXT    NOT NULL DEFAULT '',
    status          TEXT    NOT NULL,               -- running | success | error
    error           TEXT    NOT NULL DEFAULT '',
    duration_ms     INTEGER NOT NULL DEFAULT 0,
    cost_usd        REAL    NOT NULL DEFAULT 0,
    started_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    finished_at     TEXT
);
CREATE INDEX idx_runs_user ON runs(user_id, id);
