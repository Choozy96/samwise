-- Multi-agent setup: a user can have several agents, each a named persona
-- (identity + optional model/runtime override). The "soul" is the agent's system
-- prompt; empty falls back to the base assistant prompt. Conversations are bound
-- to an agent so each persona has its own thread per channel; the active agent is
-- a per-user setting.

CREATE TABLE agents (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    soul        TEXT    NOT NULL DEFAULT '',  -- system prompt; '' = base assistant prompt
    model       TEXT    NOT NULL DEFAULT '',  -- optional model id override ('' = chat hint/default)
    runtime     TEXT    NOT NULL DEFAULT '',  -- optional runtime override ('' = user's active_runtime)
    is_default  INTEGER NOT NULL DEFAULT 0,
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_agents_user ON agents(user_id);

-- Nullable plain columns (no FK clause, to satisfy SQLite ADD COLUMN rules).
ALTER TABLE conversations ADD COLUMN agent_id INTEGER;
ALTER TABLE user_settings ADD COLUMN active_agent_id INTEGER NOT NULL DEFAULT 0;

-- Seed a default agent for each existing user.
INSERT INTO agents(user_id, name, description, soul, is_default)
    SELECT id, 'Assistant', 'Your general-purpose assistant.', '', 1 FROM users;

-- Bind existing conversations + the active agent to each user's default agent.
UPDATE conversations SET agent_id = (
    SELECT a.id FROM agents a WHERE a.user_id = conversations.user_id AND a.is_default = 1
) WHERE agent_id IS NULL;

UPDATE user_settings SET active_agent_id = (
    SELECT a.id FROM agents a WHERE a.user_id = user_settings.user_id AND a.is_default = 1
) WHERE active_agent_id = 0;
