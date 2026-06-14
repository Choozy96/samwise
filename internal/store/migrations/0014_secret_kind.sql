-- A secret is either 'value' (a plain token injected as an env var) or 'oauth'
-- (a token JSON blob — still injected as an env var, but the app also parses it
-- read-only to show its expiry; it is never written back / refreshed by the app).
ALTER TABLE user_secrets ADD COLUMN kind TEXT NOT NULL DEFAULT 'value';
