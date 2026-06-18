-- Per-user opt-in for advanced agent tools, layered on top of the deployment's
-- ALLOW_AGENT_TOOLS master switch.
--   web_tools       — WebFetch + WebSearch (the assistant can fetch URLs / search)
--   dangerous_tools — ALL built-in tools (subagents, notebooks, etc.) — risky
ALTER TABLE user_settings ADD COLUMN web_tools       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE user_settings ADD COLUMN dangerous_tools INTEGER NOT NULL DEFAULT 0;
