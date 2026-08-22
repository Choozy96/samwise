-- System-wide (deployment-level) settings, admin-configured. First use: the
-- "anchor chat" — a designated announcement chat where scheduled results land
-- by default and broadcasts go.
CREATE TABLE IF NOT EXISTS system_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);
