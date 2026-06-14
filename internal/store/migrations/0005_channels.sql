-- Channel identities + pairing (spec §4.1). A channel_identity links an external
-- sender (e.g. a Telegram user) to an app user. Pairing codes are short-lived,
-- issued by the bot to an unknown sender and redeemed in the portal.

CREATE TABLE channel_identities (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel     TEXT    NOT NULL,            -- telegram
    external_id TEXT    NOT NULL,            -- sender id on that channel (string)
    chat_id     TEXT    NOT NULL,            -- where to deliver replies
    created_at  TEXT    NOT NULL DEFAULT (datetime('now')),
    UNIQUE(channel, external_id)
);
CREATE INDEX idx_chident_user ON channel_identities(user_id, channel);

CREATE TABLE pairing_codes (
    code        TEXT PRIMARY KEY,
    channel     TEXT NOT NULL,
    external_id TEXT NOT NULL,
    chat_id     TEXT NOT NULL,
    expires_at  TEXT NOT NULL
);
