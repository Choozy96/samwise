-- Slack apps ("bots") per user, bound to an agent — the Slack analog of
-- telegram_bots. A Slack app carries TWO credentials: the bot token (xoxb-…,
-- API calls) and the app-level token (xapp-…, Socket Mode connection). Both are
-- AES-GCM blobs (MASTER_KEY box); the store never decrypts.
CREATE TABLE slack_bots (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label         TEXT    NOT NULL,
    bot_token_enc TEXT    NOT NULL,
    app_token_enc TEXT    NOT NULL,
    bot_user_id   TEXT    NOT NULL DEFAULT '',  -- cached from auth.test (U…), for mention detection
    team_name     TEXT    NOT NULL DEFAULT '',  -- cached workspace name, for display
    agent_id      INTEGER REFERENCES agents(id) ON DELETE SET NULL,
    enabled       INTEGER NOT NULL DEFAULT 1,
    created_at    TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_slack_bots_user ON slack_bots(user_id);
