-- Distillation options: where the end-of-day note notification goes
-- ('' = the user's default delivery channel, 'web', or 'tg:<botID>:<chatID>'),
-- and which timezone the distillation day/schedule is anchored to
-- ('' = the user's timezone, else a fixed IANA zone).
ALTER TABLE user_settings ADD COLUMN distill_notify_target TEXT NOT NULL DEFAULT '';
ALTER TABLE user_settings ADD COLUMN distill_tz TEXT NOT NULL DEFAULT '';
