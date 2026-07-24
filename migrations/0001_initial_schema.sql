-- Initial SQLite Schema for Minimal Router OS
-- Per ARCHITECTURE.md §4.4 and ADR 0001

CREATE TABLE IF NOT EXISTS config_revisions (
    revision INTEGER PRIMARY KEY AUTOINCREMENT,
    updated_at DATETIME NOT NULL,
    config_json TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS snapshots (
    id TEXT PRIMARY KEY,
    revision INTEGER NOT NULL,
    created_at DATETIME NOT NULL,
    checksum TEXT NOT NULL,
    config_json TEXT NOT NULL,
    FOREIGN KEY(revision) REFERENCES config_revisions(revision)
);

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    csrf_token TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    last_seen DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_events (
    id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    actor TEXT NOT NULL,
    timestamp DATETIME NOT NULL,
    details_json TEXT NOT NULL
);
