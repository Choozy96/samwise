-- Generalize the audit log from tool-calls-only to an event log.
-- event_type categorizes the row: tool | auth | message | skill | job. Existing
-- rows were all tool calls, hence the default. tool_name now doubles as the
-- event's action/name (the tool name, the channel, the job name, etc.).

ALTER TABLE audit_log ADD COLUMN event_type TEXT NOT NULL DEFAULT 'tool';
CREATE INDEX idx_audit_type ON audit_log(user_id, event_type);
