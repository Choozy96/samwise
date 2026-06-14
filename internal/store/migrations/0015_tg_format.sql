-- Per-user Telegram message format. 'markdown' (default) keeps the current
-- behavior: the agent's markdown is sent raw — it renders on the web client but
-- shows literal **bold** on Telegram. 'telegram' converts the markdown to the
-- HTML subset Telegram renders (parse_mode=HTML) so bold/italic/code/links show
-- properly. Switchable any time in Settings.
ALTER TABLE user_settings ADD COLUMN tg_format TEXT NOT NULL DEFAULT 'markdown';
