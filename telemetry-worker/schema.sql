CREATE TABLE IF NOT EXISTS country_stats (
  country TEXT PRIMARY KEY,
  installs INTEGER NOT NULL DEFAULT 0,
  commands INTEGER NOT NULL DEFAULT 0,
  raw_bytes INTEGER NOT NULL DEFAULT 0,
  emitted_bytes INTEGER NOT NULL DEFAULT 0,
  bytes_saved INTEGER NOT NULL DEFAULT 0,
  tokens_saved INTEGER NOT NULL DEFAULT 0,
  reports INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS program_stats (
  program TEXT PRIMARY KEY,
  runs INTEGER NOT NULL DEFAULT 0,
  raw_bytes INTEGER NOT NULL DEFAULT 0,
  emitted_bytes INTEGER NOT NULL DEFAULT 0,
  bytes_saved INTEGER NOT NULL DEFAULT 0,
  tokens_saved INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS country_program_stats (
  country TEXT NOT NULL,
  program TEXT NOT NULL,
  runs INTEGER NOT NULL DEFAULT 0,
  raw_bytes INTEGER NOT NULL DEFAULT 0,
  emitted_bytes INTEGER NOT NULL DEFAULT 0,
  bytes_saved INTEGER NOT NULL DEFAULT 0,
  tokens_saved INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (country, program)
);

-- Per-agent breakdown: savings attributed to the invoking agent (claude, codex,
-- ...). Agent type is a category, not an identity, so this stays anonymous and
-- aggregate, the same as program_stats. Token-only; no dollar figures.
CREATE TABLE IF NOT EXISTS agent_stats (
  agent TEXT PRIMARY KEY,
  runs INTEGER NOT NULL DEFAULT 0,
  raw_bytes INTEGER NOT NULL DEFAULT 0,
  emitted_bytes INTEGER NOT NULL DEFAULT 0,
  bytes_saved INTEGER NOT NULL DEFAULT 0,
  tokens_saved INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL
);

-- Per-agent install counter: how many machines configured each agent via
-- `ctx-wire init <agent>`. Anonymous and aggregate, mirroring agent_stats. The
-- machine count lives in country_stats.installs, so this can sum higher.
CREATE TABLE IF NOT EXISTS agent_install_stats (
  agent TEXT PRIMARY KEY,
  installs INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL
);
-- Per-version aggregates so the dashboard can chart filter effectiveness across
-- releases (e.g. cargo's savings in 0.1.16 vs 0.1.17). Token-only, anonymous,
-- aggregate. Clients before 0.1.17 send no version and are bucketed "pre-0.1.17"
-- by the Worker. Safe to run on an existing database.
CREATE TABLE IF NOT EXISTS version_stats (
  version TEXT PRIMARY KEY,
  commands INTEGER NOT NULL DEFAULT 0,
  raw_bytes INTEGER NOT NULL DEFAULT 0,
  emitted_bytes INTEGER NOT NULL DEFAULT 0,
  bytes_saved INTEGER NOT NULL DEFAULT 0,
  tokens_saved INTEGER NOT NULL DEFAULT 0,
  reports INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL
);

-- Per-version, per-program savings: the data behind "did this filter improve
-- across releases". Mirrors program_stats with a version dimension.
CREATE TABLE IF NOT EXISTS version_program_stats (
  version TEXT NOT NULL,
  program TEXT NOT NULL,
  runs INTEGER NOT NULL DEFAULT 0,
  raw_bytes INTEGER NOT NULL DEFAULT 0,
  emitted_bytes INTEGER NOT NULL DEFAULT 0,
  bytes_saved INTEGER NOT NULL DEFAULT 0,
  tokens_saved INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (version, program)
);

-- Query "this program across all versions" efficiently for the per-filter chart.
CREATE INDEX IF NOT EXISTS idx_version_program_stats_program ON version_program_stats (program);
