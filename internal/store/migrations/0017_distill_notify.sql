-- Whether to notify the user with a message after the end-of-day memory
-- distillation (default on). When off, distillation stays silent.
ALTER TABLE user_settings ADD COLUMN distill_notify INTEGER NOT NULL DEFAULT 1;
