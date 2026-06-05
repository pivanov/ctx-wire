-- Per-agent savings breakdown (claude, codex, ...). Token-only, anonymous,
-- aggregate, mirroring program_stats. Safe to run on an existing database.
CREATE TABLE IF NOT EXISTS agent_stats (
  agent TEXT PRIMARY KEY,
  runs INTEGER NOT NULL DEFAULT 0,
  raw_bytes INTEGER NOT NULL DEFAULT 0,
  emitted_bytes INTEGER NOT NULL DEFAULT 0,
  bytes_saved INTEGER NOT NULL DEFAULT 0,
  tokens_saved INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL
);
