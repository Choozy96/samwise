-- Multiple Telegram bots per user (each a distinct channel), bound to an agent.
-- The legacy single .env bot keeps bot_id = 0; DB-managed bots get bot_id > 0.

-- Per-user bot tokens. token_enc is the AES-GCM blob (MASTER_KEY box), like
-- user_secrets — the store never decrypts. agent_id binds the bot to one of the
-- user's agents (NULL => the user's active agent, legacy behavior).
CREATE TABLE telegram_bots (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label      TEXT    NOT NULL,
    token_enc  TEXT    NOT NULL,
    username   TEXT    NOT NULL DEFAULT '',          -- cached @username from getMe
    agent_id   INTEGER REFERENCES agents(id) ON DELETE SET NULL,
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_telegram_bots_user ON telegram_bots(user_id);

-- A pairing code is now scoped to the bot the unknown sender messaged.
ALTER TABLE pairing_codes ADD COLUMN bot_id INTEGER NOT NULL DEFAULT 0;

-- Rebuild channel_identities to widen the UNIQUE to include bot_id, so one
-- Telegram account can pair to several bots (one identity row per bot). Nothing
-- references this table by FK, so a straight rebuild is safe. Existing rows are
-- the legacy bot (bot_id = 0).
CREATE TABLE channel_identities_new (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel     TEXT    NOT NULL,
    bot_id      INTEGER NOT NULL DEFAULT 0,
    external_id TEXT    NOT NULL,
    chat_id     TEXT    NOT NULL,
    created_at  TEXT    NOT NULL DEFAULT (datetime('now')),
    UNIQUE(channel, bot_id, external_id)
);
INSERT INTO channel_identities_new (id, user_id, channel, bot_id, external_id, chat_id, created_at)
    SELECT id, user_id, channel, 0, external_id, chat_id, created_at FROM channel_identities;
DROP TABLE channel_identities;
ALTER TABLE channel_identities_new RENAME TO channel_identities;
CREATE INDEX idx_chident_user ON channel_identities(user_id, channel);
