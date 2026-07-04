-- Make a skill executable: an entrypoint script (relative to its bundle) that
-- skill_run invokes directly (no shell). Empty entrypoint = not runnable.
-- env_keys is a comma-separated allowlist of the user's secret names this skill's
-- script may receive as env vars (only those, never the whole secret set).
ALTER TABLE skills ADD COLUMN entrypoint TEXT NOT NULL DEFAULT '';
ALTER TABLE skills ADD COLUMN env_keys TEXT NOT NULL DEFAULT '';
