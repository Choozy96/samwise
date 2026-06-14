-- First-run onboarding flag. New users start un-onboarded (column default 0) and
-- are sent through the setup wizard on first login; existing users have already
-- configured things, so mark them onboarded.
ALTER TABLE user_settings ADD COLUMN onboarded INTEGER NOT NULL DEFAULT 0;
UPDATE user_settings SET onboarded = 1;
