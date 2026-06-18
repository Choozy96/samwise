-- Per-user (or global) MCP server registry. Enabled entries are
-- composed into each run's --mcp-config alongside the core server. Credentials
-- (env vars, http headers, API tokens, SA JSON) are encrypted at rest under
-- MASTER_KEY via internal/secretbox; only secret_enc is sensitive.

CREATE TABLE mcp_servers (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER REFERENCES users(id) ON DELETE CASCADE, -- NULL = global (admin-managed)
    name       TEXT    NOT NULL,                 -- becomes the mcp__<name>__ tool prefix
    transport  TEXT    NOT NULL,                 -- stdio | http
    command    TEXT    NOT NULL DEFAULT '',      -- stdio: executable
    args_json  TEXT    NOT NULL DEFAULT '[]',    -- stdio: JSON array of args
    url        TEXT    NOT NULL DEFAULT '',      -- http: endpoint
    secret_enc TEXT    NOT NULL DEFAULT '',      -- AES-GCM blob of {"env":{...},"headers":{...}}
    tools_json TEXT    NOT NULL DEFAULT '[]',    -- tool names discovered at registration
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_mcp_user ON mcp_servers(user_id, enabled);
