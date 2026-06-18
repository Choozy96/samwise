-- Per-user opt-in agent tools as a comma-separated list of tool names (chosen
-- individually in settings), replacing the coarse web_tools/dangerous_tools flags
-- from 0020 (those columns are left in place, unused, since SQLite can't drop a
-- column without a table rebuild).
ALTER TABLE user_settings ADD COLUMN extra_tools TEXT NOT NULL DEFAULT '';
