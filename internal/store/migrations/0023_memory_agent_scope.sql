-- Per-agent scope for SEMANTIC memory. agent_id NULL = user memory (loaded by all
-- of a user's agents); agent_id = N = memory specific to agent N (persona facts).
-- Episodic (dated daily notes) stays user-scoped. SQLite can't ALTER an FTS5
-- table's columns, so memory_fts is rebuilt with agent_id and repopulated.

ALTER TABLE memory_semantic ADD COLUMN agent_id INTEGER;
CREATE INDEX idx_memsem_user_agent ON memory_semantic(user_id, agent_id);

DROP TRIGGER memsem_ai;
DROP TRIGGER memsem_ad;
DROP TRIGGER memsem_au;
DROP TRIGGER memepi_ai;
DROP TRIGGER memepi_ad;
DROP TABLE memory_fts;

CREATE VIRTUAL TABLE memory_fts USING fts5(
    content, topic, kind,
    layer    UNINDEXED,
    ref_id   UNINDEXED,
    user_id  UNINDEXED,
    agent_id UNINDEXED,
    ts       UNINDEXED,
    tokenize = 'porter unicode61'
);

-- Repopulate from the base tables (semantic carries its agent_id; episodic NULL).
INSERT INTO memory_fts(content, topic, kind, layer, ref_id, user_id, agent_id, ts)
    SELECT content, topic, kind, 'semantic', id, user_id, agent_id, created_at FROM memory_semantic;
INSERT INTO memory_fts(content, topic, kind, layer, ref_id, user_id, agent_id, ts)
    SELECT content, period_date, period_type, 'episodic', id, user_id, NULL, period_date FROM memory_episodic;

CREATE TRIGGER memsem_ai AFTER INSERT ON memory_semantic BEGIN
    INSERT INTO memory_fts(content, topic, kind, layer, ref_id, user_id, agent_id, ts)
    VALUES (new.content, new.topic, new.kind, 'semantic', new.id, new.user_id, new.agent_id, new.created_at);
END;
CREATE TRIGGER memsem_ad AFTER DELETE ON memory_semantic BEGIN
    DELETE FROM memory_fts WHERE layer = 'semantic' AND ref_id = old.id;
END;
CREATE TRIGGER memsem_au AFTER UPDATE ON memory_semantic BEGIN
    DELETE FROM memory_fts WHERE layer = 'semantic' AND ref_id = old.id;
    INSERT INTO memory_fts(content, topic, kind, layer, ref_id, user_id, agent_id, ts)
    VALUES (new.content, new.topic, new.kind, 'semantic', new.id, new.user_id, new.agent_id, new.created_at);
END;

CREATE TRIGGER memepi_ai AFTER INSERT ON memory_episodic BEGIN
    INSERT INTO memory_fts(content, topic, kind, layer, ref_id, user_id, agent_id, ts)
    VALUES (new.content, new.period_date, new.period_type, 'episodic', new.id, new.user_id, NULL, new.period_date);
END;
CREATE TRIGGER memepi_ad AFTER DELETE ON memory_episodic BEGIN
    DELETE FROM memory_fts WHERE layer = 'episodic' AND ref_id = old.id;
END;
