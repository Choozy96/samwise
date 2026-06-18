-- How a Telegram bot replies in group chats: 'mention' (only when @mentioned, a
-- reply to it, or a command — the default) or 'all' (respond to every message,
-- which also needs the bot's Telegram privacy mode turned off in BotFather).
ALTER TABLE user_settings ADD COLUMN group_reply_mode TEXT NOT NULL DEFAULT 'mention';
