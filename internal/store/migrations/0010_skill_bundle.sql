-- has_bundle marks a skill that has files (scripts/assets) on disk under the
-- user's workspace .claude/skills/<name>/ (from a ZIP import), not just inline
-- SKILL.md content.
ALTER TABLE skills ADD COLUMN has_bundle INTEGER NOT NULL DEFAULT 0;
