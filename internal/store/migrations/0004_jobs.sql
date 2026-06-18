-- Scheduler jobs. DB-backed, evaluated by a 1-minute tick loop;
-- survives restarts because all state is here. next_fire_utc is materialized
--: recomputed on timezone change, not evaluated every tick.

CREATE TABLE jobs (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id           INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name              TEXT    NOT NULL DEFAULT '',
    type              TEXT    NOT NULL,                 -- agent_run | direct_message | maintenance
    schedule_spec     TEXT    NOT NULL,                 -- once@<localISO> | daily@HH:MM | weekly@DOW HH:MM
    tz_mode           TEXT    NOT NULL DEFAULT 'user_local', -- user_local | fixed_tz | fixed_utc
    tz_ref            TEXT    NOT NULL DEFAULT '',       -- IANA name when fixed_tz
    payload           TEXT    NOT NULL DEFAULT '{}',     -- JSON: prompt/message/maintenance kind
    enabled           INTEGER NOT NULL DEFAULT 1,
    catch_up          INTEGER NOT NULL DEFAULT 1,
    next_fire_utc     TEXT,                              -- materialized RFC3339 UTC
    last_fired_period TEXT    NOT NULL DEFAULT '',       -- e.g. local date '2026-06-10'
    created_at        TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_jobs_due ON jobs(enabled, next_fire_utc);
CREATE INDEX idx_jobs_user ON jobs(user_id);
