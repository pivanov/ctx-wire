// RateLimiter is Cloudflare's native rate-limit binding surface (configured in
// wrangler.toml). Declared locally so the worker typechecks without extra deps.
interface RateLimiter {
  limit(options: { key: string }): Promise<{ success: boolean }>;
}

export interface Env {
  ctx_wire_telemetry: D1Database;
  // Optional so the worker still runs in local/dev or test contexts where the
  // binding is absent; when present, write requests are rate-limited per IP.
  WRITE_RATE_LIMITER?: RateLimiter;
}

type TelemetryEvent = "install" | "impact";

type ProgramValue =
  | number
  | {
      runs?: unknown;
      count?: unknown;
      raw_bytes?: unknown;
      emitted_bytes?: unknown;
      bytes_saved?: unknown;
      tokens_saved?: unknown;
    };

interface TelemetryPayload {
  schema?: unknown;
  event?: unknown;
  commands?: unknown;
  raw_bytes?: unknown;
  emitted_bytes?: unknown;
  bytes_saved?: unknown;
  tokens_saved?: unknown;
  programs?: unknown;
  agents?: unknown;
  // install-event fields: the configured agent (claude, codex, ...) and whether
  // this event should increment the aggregate install counter. Absent on impact
  // events.
  agent?: unknown;
  machine?: unknown;
}

const MAX_BODY_BYTES = 16 * 1024;
const MAX_COMMANDS = 1_000_000;
const MAX_RAW_BYTES = 1024 * 1024 * 1024;
const MAX_EMITTED_BYTES = 1024 * 1024 * 1024;
const MAX_BYTES_SAVED = 1024 * 1024 * 1024;
const MAX_TOKENS_SAVED = 300_000_000;
const MAX_PROGRAMS = 50;
const MAX_AGENTS = 20;
const MAX_PROGRAM_RUNS = 100_000;
const PROGRAM_RE = /^[a-z0-9._+-]{1,64}$/;

const corsHeaders = {
  "Access-Control-Allow-Origin": "*",
  "Access-Control-Allow-Methods": "GET,POST,OPTIONS",
  "Access-Control-Allow-Headers": "Content-Type",
};

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);

    if (request.method === "OPTIONS") {
      return new Response(null, { status: 204, headers: corsHeaders });
    }

    if (request.method === "GET" && url.pathname === "/health") {
      return json({ ok: true });
    }

    if (
      request.method === "GET" &&
      (url.pathname === "/v1/impact" || url.pathname === "/v1/stats")
    ) {
      return readStats(env);
    }

    if (
      request.method === "POST" &&
      (url.pathname === "/v1/telemetry" || url.pathname === "/v1/impact")
    ) {
      return writeTelemetry(request, env);
    }

    return json({ error: "not_found" }, 404);
  },
};

async function writeTelemetry(request: Request, env: Env): Promise<Response> {
  // Bound write volume per client IP (cheapest possible reject, before parsing).
  // Fail-open if the binding is missing so a misconfiguration never drops legit
  // reports. Cloudflare's edge DDoS/bot protection covers the cruder cases.
  if (env.WRITE_RATE_LIMITER) {
    const ip = request.headers.get("CF-Connecting-IP") ?? "unknown";
    const { success } = await env.WRITE_RATE_LIMITER.limit({ key: ip });
    if (!success) {
      return json({ error: "rate_limited" }, 429);
    }
  }

  const contentLength = Number(request.headers.get("content-length") || "0");
  if (contentLength > MAX_BODY_BYTES) {
    return json({ error: "body_too_large" }, 413);
  }

  const text = await request.text();
  if (text.length > MAX_BODY_BYTES) {
    return json({ error: "body_too_large" }, 413);
  }

  let payload: TelemetryPayload;
  try {
    payload = JSON.parse(text) as TelemetryPayload;
  } catch {
    return json({ error: "invalid_json" }, 400);
  }

  if (payload.schema !== 1) {
    return json({ error: "unsupported_schema" }, 400);
  }

  const event = parseEvent(payload.event);
  if (!event) {
    return json({ error: "invalid_event" }, 400);
  }

  const country = normalizeCountry(request.cf?.country);
  const now = new Date().toISOString();

  if (event === "install") {
    await recordInstall(env, country, now, payload);
    return json({ ok: true });
  }

  const commands = clampInt(payload.commands, 0, MAX_COMMANDS);
  const rawBytes = clampInt(payload.raw_bytes, 0, MAX_RAW_BYTES);
  const emittedBytes = clampInt(payload.emitted_bytes, 0, MAX_EMITTED_BYTES);
  const bytesSaved = clampInt(payload.bytes_saved, 0, MAX_BYTES_SAVED);
  const tokensSaved = clampInt(payload.tokens_saved, 0, MAX_TOKENS_SAVED);
  const programs = parsePrograms(payload.programs);
  const agents = parseAgents(payload.agents);

  if (
    commands === 0 &&
    rawBytes === 0 &&
    emittedBytes === 0 &&
    bytesSaved === 0 &&
    tokensSaved === 0 &&
    programs.length === 0 &&
    agents.length === 0
  ) {
    return json({ error: "empty_impact" }, 400);
  }

  const statements: D1PreparedStatement[] = [
    env.ctx_wire_telemetry
      .prepare(
        `INSERT INTO country_stats (country, commands, raw_bytes, emitted_bytes, bytes_saved, tokens_saved, reports, updated_at)
         VALUES (?, ?, ?, ?, ?, ?, 1, ?)
         ON CONFLICT(country) DO UPDATE SET
           commands = commands + excluded.commands,
           raw_bytes = raw_bytes + excluded.raw_bytes,
           emitted_bytes = emitted_bytes + excluded.emitted_bytes,
           bytes_saved = bytes_saved + excluded.bytes_saved,
           tokens_saved = tokens_saved + excluded.tokens_saved,
           reports = reports + 1,
           updated_at = excluded.updated_at`,
      )
      .bind(country, commands, rawBytes, emittedBytes, bytesSaved, tokensSaved, now),
  ];

  for (const program of programs) {
    statements.push(
      env.ctx_wire_telemetry
        .prepare(
          `INSERT INTO program_stats (program, runs, raw_bytes, emitted_bytes, bytes_saved, tokens_saved, updated_at)
           VALUES (?, ?, ?, ?, ?, ?, ?)
           ON CONFLICT(program) DO UPDATE SET
             runs = runs + excluded.runs,
             raw_bytes = raw_bytes + excluded.raw_bytes,
             emitted_bytes = emitted_bytes + excluded.emitted_bytes,
             bytes_saved = bytes_saved + excluded.bytes_saved,
             tokens_saved = tokens_saved + excluded.tokens_saved,
             updated_at = excluded.updated_at`,
        )
        .bind(
          program.name,
          program.runs,
          program.rawBytes,
          program.emittedBytes,
          program.bytesSaved,
          program.tokensSaved,
          now,
        ),
    );
    statements.push(
      env.ctx_wire_telemetry
        .prepare(
          `INSERT INTO country_program_stats (country, program, runs, raw_bytes, emitted_bytes, bytes_saved, tokens_saved, updated_at)
           VALUES (?, ?, ?, ?, ?, ?, ?, ?)
           ON CONFLICT(country, program) DO UPDATE SET
             runs = runs + excluded.runs,
             raw_bytes = raw_bytes + excluded.raw_bytes,
             emitted_bytes = emitted_bytes + excluded.emitted_bytes,
             bytes_saved = bytes_saved + excluded.bytes_saved,
             tokens_saved = tokens_saved + excluded.tokens_saved,
             updated_at = excluded.updated_at`,
        )
        .bind(
          country,
          program.name,
          program.runs,
          program.rawBytes,
          program.emittedBytes,
          program.bytesSaved,
          program.tokensSaved,
          now,
        ),
    );
  }

  for (const agent of agents) {
    statements.push(
      env.ctx_wire_telemetry
        .prepare(
          `INSERT INTO agent_stats (agent, runs, raw_bytes, emitted_bytes, bytes_saved, tokens_saved, updated_at)
           VALUES (?, ?, ?, ?, ?, ?, ?)
           ON CONFLICT(agent) DO UPDATE SET
             runs = runs + excluded.runs,
             raw_bytes = raw_bytes + excluded.raw_bytes,
             emitted_bytes = emitted_bytes + excluded.emitted_bytes,
             bytes_saved = bytes_saved + excluded.bytes_saved,
             tokens_saved = tokens_saved + excluded.tokens_saved,
             updated_at = excluded.updated_at`,
        )
        .bind(
          agent.name,
          agent.runs,
          agent.rawBytes,
          agent.emittedBytes,
          agent.bytesSaved,
          agent.tokensSaved,
          now,
        ),
    );
  }

  await env.ctx_wire_telemetry.batch(statements);
  return json({ ok: true });
}

// recordInstall counts an install. The machine flag bumps the per-country
// reported-install counter; a configured agent bumps the per-agent counter.
// Modern clients send both on every successful `ctx-wire init <agent>`. Legacy
// install events (no agent, no machine field) carry no agent, so they only count
// as machine installs.
async function recordInstall(
  env: Env,
  country: string,
  now: string,
  payload: TelemetryPayload,
): Promise<void> {
  const agent = parseInstallAgent(payload.agent);
  const countReportedInstall = payload.machine === true;
  const legacyInstall = payload.machine === undefined && payload.agent === undefined;
  const statements: D1PreparedStatement[] = [];

  if (countReportedInstall || legacyInstall) {
    statements.push(
      env.ctx_wire_telemetry
        .prepare(
          `INSERT INTO country_stats (country, installs, updated_at)
           VALUES (?, 1, ?)
           ON CONFLICT(country) DO UPDATE SET
             installs = installs + 1,
             updated_at = excluded.updated_at`,
        )
        .bind(country, now),
    );
  }
  if (agent !== "") {
    statements.push(
      env.ctx_wire_telemetry
        .prepare(
          `INSERT INTO agent_install_stats (agent, installs, updated_at)
           VALUES (?, 1, ?)
           ON CONFLICT(agent) DO UPDATE SET
             installs = installs + 1,
             updated_at = excluded.updated_at`,
        )
        .bind(agent, now),
    );
  }

  if (statements.length === 0) {
    return;
  }
  if (statements.length === 1) {
    await statements[0].run();
  } else {
    await env.ctx_wire_telemetry.batch(statements);
  }
}

// parseInstallAgent validates the install event's agent name with the same
// charset as program/agent buckets, returning "" for absent or malformed values.
function parseInstallAgent(value: unknown): string {
  if (typeof value !== "string") {
    return "";
  }
  const name = value.trim().toLowerCase();
  return PROGRAM_RE.test(name) ? name : "";
}

async function readStats(env: Env): Promise<Response> {
  const [totals, countries, programs, countryPrograms, agents, agentInstalls] = await Promise.all([
    env.ctx_wire_telemetry
      .prepare(
        `SELECT
           COALESCE(SUM(installs), 0) AS installs,
           COALESCE(SUM(commands), 0) AS commands,
           COALESCE(SUM(raw_bytes), 0) AS raw_bytes,
           COALESCE(SUM(emitted_bytes), 0) AS emitted_bytes,
           COALESCE(SUM(bytes_saved), 0) AS bytes_saved,
           COALESCE(SUM(tokens_saved), 0) AS tokens_saved,
           COALESCE(SUM(reports), 0) AS reports
         FROM country_stats`,
      )
      .first(),
    env.ctx_wire_telemetry
      .prepare(
        `SELECT country, installs, bytes_saved, tokens_saved, reports, updated_at
         , commands, raw_bytes, emitted_bytes
         FROM country_stats
         ORDER BY tokens_saved DESC, bytes_saved DESC
         LIMIT 250`,
      )
      .all(),
    env.ctx_wire_telemetry
      .prepare(
        `SELECT program, runs, bytes_saved, tokens_saved, updated_at
         , raw_bytes, emitted_bytes
         FROM program_stats
         ORDER BY runs DESC
         LIMIT 100`,
      )
      .all(),
    env.ctx_wire_telemetry
      .prepare(
        `SELECT country, program, runs, bytes_saved, tokens_saved, updated_at
         , raw_bytes, emitted_bytes
         FROM country_program_stats
         ORDER BY runs DESC
         LIMIT 500`,
      )
      .all(),
    env.ctx_wire_telemetry
      .prepare(
        `SELECT agent, runs, bytes_saved, tokens_saved, updated_at
         , raw_bytes, emitted_bytes
         FROM agent_stats
         ORDER BY tokens_saved DESC, runs DESC
         LIMIT 50`,
      )
      .all(),
    env.ctx_wire_telemetry
      .prepare(
        `SELECT agent, installs, updated_at
         FROM agent_install_stats
         ORDER BY installs DESC
         LIMIT 50`,
      )
      .all(),
  ]);

  return json({
    schema: 1,
    totals: totals || {
      installs: 0,
      commands: 0,
      raw_bytes: 0,
      emitted_bytes: 0,
      bytes_saved: 0,
      tokens_saved: 0,
      reports: 0,
    },
    countries: countries.results,
    programs: programs.results,
    country_programs: countryPrograms.results,
    agents: agents.results,
    agent_installs: agentInstalls.results,
  });
}

function parseEvent(value: unknown): TelemetryEvent | "" {
  return value === "install" || value === "impact" ? value : "";
}

function normalizeCountry(value: unknown): string {
  if (typeof value !== "string") {
    return "XX";
  }
  const country = value.toUpperCase();
  return /^[A-Z]{2}$/.test(country) ? country : "XX";
}

interface Bucket {
  name: string;
  runs: number;
  bytesSaved: number;
  rawBytes: number;
  emittedBytes: number;
  tokensSaved: number;
}

function parsePrograms(value: unknown): Bucket[] {
  return parseBuckets(value, MAX_PROGRAMS);
}

function parseAgents(value: unknown): Bucket[] {
  return parseBuckets(value, MAX_AGENTS);
}

// parseBuckets validates a {name: count|object} map (programs or agents) into a
// capped, sanitized list. The same name regex and counter clamps apply to both,
// so an agent breakdown is validated exactly like a program breakdown.
function parseBuckets(value: unknown, maxEntries: number): Bucket[] {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return [];
  }

  const buckets: Bucket[] = [];

  for (const [rawName, rawValue] of Object.entries(value as Record<string, ProgramValue>)) {
    if (buckets.length >= maxEntries) {
      break;
    }
    const name = rawName.trim().toLowerCase();
    if (!PROGRAM_RE.test(name)) {
      continue;
    }

    if (typeof rawValue === "number") {
      const runs = clampInt(rawValue, 0, MAX_PROGRAM_RUNS);
      if (runs > 0) {
        buckets.push({ name, runs, rawBytes: 0, emittedBytes: 0, bytesSaved: 0, tokensSaved: 0 });
      }
      continue;
    }

    if (!rawValue || typeof rawValue !== "object" || Array.isArray(rawValue)) {
      continue;
    }

    const runs = clampInt(rawValue.runs ?? rawValue.count, 0, MAX_PROGRAM_RUNS);
    const rawBytes = clampInt(rawValue.raw_bytes, 0, MAX_RAW_BYTES);
    const emittedBytes = clampInt(rawValue.emitted_bytes, 0, MAX_EMITTED_BYTES);
    const bytesSaved = clampInt(rawValue.bytes_saved, 0, MAX_BYTES_SAVED);
    const tokensSaved = clampInt(rawValue.tokens_saved, 0, MAX_TOKENS_SAVED);
    if (runs > 0 || rawBytes > 0 || emittedBytes > 0 || bytesSaved > 0 || tokensSaved > 0) {
      buckets.push({ name, runs, rawBytes, emittedBytes, bytesSaved, tokensSaved });
    }
  }

  return buckets;
}

function clampInt(value: unknown, min: number, max: number): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return min;
  }
  return Math.max(min, Math.min(max, Math.trunc(value)));
}

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: {
      ...corsHeaders,
      "Content-Type": "application/json; charset=utf-8",
    },
  });
}
