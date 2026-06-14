-- Memory system (spec §6) + audit log (spec §10.5).
--
-- Semantic = discrete facts/preferences/events. Episodic = dated distillations.
-- Retrieval is SQLite FTS5 over both layers. A nullable embedding BLOB is
-- present on each so sqlite-vec semantic search is a later migration-free
-- iteration (spec §14). Every row is user-scoped.

CREATE TABLE memory_semantic (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    topic      TEXT    NOT NULL DEFAULT '',
    kind       TEXT    NOT NULL,                 -- fact | preference | event
    content    TEXT    NOT NULL,
    source     TEXT    NOT NULL DEFAULT '',
    created_at TEXT    NOT NULL DEFAULT (datetime('now')),
    expires_at TEXT,
    embedding  BLOB
);
CREATE INDEX idx_memsem_user ON memory_semantic(user_id, topic);

CREATE TABLE memory_episodic (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    period_type TEXT    NOT NULL,                -- day | week
    period_date TEXT    NOT NULL,                -- local 'YYYY-MM-DD' (week: start date)
    content     TEXT    NOT NULL,
    created_at  TEXT    NOT NULL DEFAULT (datetime('now')),
    embedding   BLOB
);
CREATE INDEX idx_memepi_user ON memory_episodic(user_id, period_date);

-- Unified full-text index across both layers. Indexed columns are searchable;
-- the rest are filter/lookup metadata. ts holds created_at (semantic) or
-- period_date (episodic) for after/before range filtering.
CREATE VIRTUAL TABLE memory_fts USING fts5(
    content, topic, kind,
    layer  UNINDEXED,
    ref_id UNINDEXED,
    user_id UNINDEXED,
    ts     UNINDEXED,
    tokenize = 'porter unicode61'
);

-- Keep the FTS index in sync with the base tables via triggers.
CREATE TRIGGER memsem_ai AFTER INSERT ON memory_semantic BEGIN
    INSERT INTO memory_fts(content, topic, kind, layer, ref_id, user_id, ts)
    VALUES (new.content, new.topic, new.kind, 'semantic', new.id, new.user_id, new.created_at);
END;
CREATE TRIGGER memsem_ad AFTER DELETE ON memory_semantic BEGIN
    DELETE FROM memory_fts WHERE layer = 'semantic' AND ref_id = old.id;
END;
CREATE TRIGGER memsem_au AFTER UPDATE ON memory_semantic BEGIN
    DELETE FROM memory_fts WHERE layer = 'semantic' AND ref_id = old.id;
    INSERT INTO memory_fts(content, topic, kind, layer, ref_id, user_id, ts)
    VALUES (new.content, new.topic, new.kind, 'semantic', new.id, new.user_id, new.created_at);
END;

CREATE TRIGGER memepi_ai AFTER INSERT ON memory_episodic BEGIN
    INSERT INTO memory_fts(content, topic, kind, layer, ref_id, user_id, ts)
    VALUES (new.content, new.period_date, new.period_type, 'episodic', new.id, new.user_id, new.period_date);
END;
CREATE TRIGGER memepi_ad AFTER DELETE ON memory_episodic BEGIN
    DELETE FROM memory_fts WHERE layer = 'episodic' AND ref_id = old.id;
END;

-- Audit log: every core MCP tool call (spec §10.5). args_summary is a short,
-- non-sensitive description — never full arguments.
CREATE TABLE audit_log (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id       INTEGER NOT NULL,
    run_id        INTEGER,
    tool_name     TEXT    NOT NULL,
    args_summary  TEXT    NOT NULL DEFAULT '',
    result_status TEXT    NOT NULL,
    ts            TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_audit_user ON audit_log(user_id, id);
