-- Discord bots per user, bound to an agent — the Discord analog of slack_bots.
-- Discord needs only ONE credential (the bot token); the Gateway WebSocket uses
-- the same token. token_enc is an AES-GCM blob (MASTER_KEY box).
CREATE TABLE discord_bots (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label       TEXT    NOT NULL,
    token_enc   TEXT    NOT NULL,
    bot_user_id TEXT    NOT NULL DEFAULT '',  -- cached application bot user id, for mention detection
    username    TEXT    NOT NULL DEFAULT '',  -- cached bot username, for display
    agent_id    INTEGER REFERENCES agents(id) ON DELETE SET NULL,
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_discord_bots_user ON discord_bots(user_id);
