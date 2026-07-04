-- Per-resource audience: who in a group chat may use a skill or a built-in tool.
-- 'paired' (default) = only registered/paired users of the profile; 'everyone' =
-- also unregistered group senders (read-only runs). Exec/write tools (Bash, Write,
-- Edit) are hard-locked to 'paired' in code regardless of this setting.

-- Skills carry their audience directly.
ALTER TABLE skills ADD COLUMN audience TEXT NOT NULL DEFAULT 'paired';

-- Built-in tool audiences are a per-user JSON map of tool name -> 'everyone'
-- (only overrides are stored; unset tools use their code default — Read/Glob/Grep
-- default to 'everyone', everything else to 'paired').
ALTER TABLE user_settings ADD COLUMN tool_audience TEXT NOT NULL DEFAULT '';
