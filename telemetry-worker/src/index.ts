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
  // Much tighter second limiter for impact reports with outsized savings
  // claims, the writes that move the public counters disproportionately.
  // Optional for the same fail-open reason as above.
  HEAVY_RATE_LIMITER?: RateLimiter;
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
  version?: unknown;
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
// Reports claiming more than this are possible but rare for a genuine client
// (a 30-minute flush from the heaviest observed real workload is around this
// scale), so they additionally pass the HEAVY_RATE_LIMITER. A flood of
// ceiling-sized reports is the cheapest way to inflate the public counters;
// one such report every few minutes per IP is not.
const HEAVY_REPORT_TOKENS = 25_000_000;
const HEAVY_REPORT_BYTES = 100 * 1024 * 1024;
const MAX_PROGRAMS = 50;
const MAX_AGENTS = 20;
const MAX_PROGRAM_RUNS = 100_000;
const PROGRAM_RE = /^[a-z0-9._+-]{1,64}$/;

// Catch-all label for any agent not on the allowlist below, matching the
// client's telemetry safelist (internal/telemetry/safelist.go otherBucket). A
// well-behaved client already collapses unknown agents to "other" before
// sending, so this gate changes nothing for real traffic; it exists so a direct
// unauthenticated POST cannot put an arbitrary name on the public "saved by
// agent" leaderboard, and so agent_stats rows stay bounded to a known set.
const OTHER_BUCKET = "other";

// KNOWN_AGENTS mirrors agent.Known in internal/agent/agent.go. Keep the two in
// sync: a new agent added there must be added here, or its telemetry folds into
// "other" on the board (fails safe, never drops the data). The list is small and
// changes only when a new agent integration ships.
const KNOWN_AGENTS = new Set([
  "claude", "codex", "cursor", "gemini", "copilot", "windsurf", "cline",
  "kilocode", "antigravity", "opencode", "pi", "hermes", "vscode", "visualstudio",
  OTHER_BUCKET,
]);
// Version label for the per-version aggregates that drive "did this filter
// improve across releases" charts. A pre-0.1.17 client sends no version, bucket
// those as "pre-0.1.17"; an invalid value buckets as "unknown" so one bad
// payload never pollutes the version axis (and never rejects the whole report).
const VERSION_RE = /^[0-9a-zA-Z][0-9a-zA-Z.\-+]{0,31}$/;
function normalizeVersion(v: unknown): string {
  if (v === undefined || v === null || v === "") return "pre-0.1.17";
  if (typeof v === "string" && VERSION_RE.test(v)) return v;
  return "unknown";
}

const corsHeaders = {
  "Access-Control-Allow-Origin": "*",
  "Access-Control-Allow-Methods": "GET,POST,OPTIONS",
  "Access-Control-Allow-Headers": "Content-Type",
};

export default {
  async fetch(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
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
      return cachedStats(request, env, ctx);
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
  const clientIP = request.headers.get("CF-Connecting-IP") ?? "unknown";
  if (env.WRITE_RATE_LIMITER) {
    const { success } = await env.WRITE_RATE_LIMITER.limit({ key: clientIP });
    if (!success) {
      return json({ error: "rate_limited" }, 429);
    }
  }

  // Require a valid Content-Length and reject oversize/missing before reading the
  // body, so a client that omits the header can't force the whole body to be read
  // before rejection. The CLI always sets it (fetch/Go http with a sized body).
  const contentLength = Number(request.headers.get("content-length"));
  if (!Number.isFinite(contentLength) || contentLength <= 0 || contentLength > MAX_BODY_BYTES) {
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
    // Bounded by the ordinary limiter only. Every `ctx-wire init <agent>`
    // sends one install event with no retry, so a legit multi-agent init
    // burst (or a NAT fleet onboarding) must never be dropped; installs are
    // fixed +1 increments with no volume dimension, and inflating the counter
    // is detectable and repairable in a way faked token volume is not.
    await recordInstall(env, country, now, payload);
    return json({ ok: true });
  }

  const impact = sanitizeImpact(payload);
  const { commands, rawBytes, emittedBytes, bytesSaved, tokensSaved, programs, agents } = impact;
  const version = normalizeVersion(payload.version);

  // Surface reports that tripped a consistency guard so `wrangler tail` can
  // attribute skew attempts; the report still lands with the clamped values.
  if (impact.flags.length > 0) {
    console.log("consistency guard", country, impact.flags.join(","));
  }

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

  // Outsized savings claims pass a second, much tighter per-IP limit. The
  // standard limiter's shared-NAT headroom would let a flood of ceiling-sized
  // reports add billions of fake tokens per hour; genuine heavy reports are
  // far rarer than the limit here.
  if (
    env.HEAVY_RATE_LIMITER &&
    (tokensSaved > HEAVY_REPORT_TOKENS || bytesSaved > HEAVY_REPORT_BYTES)
  ) {
    const { success } = await env.HEAVY_RATE_LIMITER.limit({ key: clientIP });
    if (!success) {
      return json({ error: "rate_limited" }, 429);
    }
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
    env.ctx_wire_telemetry
      .prepare(
        `INSERT INTO version_stats (version, commands, raw_bytes, emitted_bytes, bytes_saved, tokens_saved, reports, updated_at)
         VALUES (?, ?, ?, ?, ?, ?, 1, ?)
         ON CONFLICT(version) DO UPDATE SET
           commands = commands + excluded.commands,
           raw_bytes = raw_bytes + excluded.raw_bytes,
           emitted_bytes = emitted_bytes + excluded.emitted_bytes,
           bytes_saved = bytes_saved + excluded.bytes_saved,
           tokens_saved = tokens_saved + excluded.tokens_saved,
           reports = reports + 1,
           updated_at = excluded.updated_at`,
      )
      .bind(version, commands, rawBytes, emittedBytes, bytesSaved, tokensSaved, now),
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
    statements.push(
      env.ctx_wire_telemetry
        .prepare(
          `INSERT INTO version_program_stats (version, program, runs, raw_bytes, emitted_bytes, bytes_saved, tokens_saved, updated_at)
           VALUES (?, ?, ?, ?, ?, ?, ?, ?)
           ON CONFLICT(version, program) DO UPDATE SET
             runs = runs + excluded.runs,
             raw_bytes = raw_bytes + excluded.raw_bytes,
             emitted_bytes = emitted_bytes + excluded.emitted_bytes,
             bytes_saved = bytes_saved + excluded.bytes_saved,
             tokens_saved = tokens_saved + excluded.tokens_saved,
             updated_at = excluded.updated_at`,
        )
        .bind(
          version,
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

// parseInstallAgent validates the install event's agent name, returning "" for
// absent or malformed values. A valid-but-unknown agent folds to "other" (the
// same allowlist the impact agent breakdown uses), so a direct install POST
// cannot write an arbitrary name into agent_install_stats.
function parseInstallAgent(value: unknown): string {
  if (typeof value !== "string") {
    return "";
  }
  const name = value.trim().toLowerCase();
  if (!PROGRAM_RE.test(name)) {
    return "";
  }
  return KNOWN_AGENTS.has(name) ? name : OTHER_BUCKET;
}

// STATS_CACHE_TTL_SECONDS caches the read-only stats response at the edge. The
// stats are cumulative aggregates that barely move minute to minute, so a few
// minutes of staleness is fine. Without this, a dashboard polling /v1/stats
// every couple of seconds re-scans every table on every hit (~1.3k D1 rows per
// call), which alone burned ~4.5M of the 5M/day free-tier read budget. With a
// 5-minute cache, D1 is read at most once per window per colo instead.
const STATS_CACHE_TTL_SECONDS = 300;

// cachedStats serves /v1/stats and /v1/impact from the edge cache, only hitting
// D1 on a miss. The cache key is normalized to origin+path so query strings and
// request headers never fragment it. cache.put runs in waitUntil so it never
// adds latency to the response.
async function cachedStats(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
  // caches.default is the Cloudflare Workers edge cache; the base DOM lib types
  // CacheStorage without it, so reach it through a narrow cast.
  const cache = (caches as unknown as { default: Cache }).default;
  const url = new URL(request.url);
  const cacheKey = new Request(`${url.origin}${url.pathname}`, { method: "GET" });

  const hit = await cache.match(cacheKey);
  if (hit) {
    const served = new Response(hit.body, hit);
    served.headers.set("X-Ctx-Cache", "hit");
    return served;
  }

  const fresh = await readStats(env);
  const cacheable = new Response(fresh.body, fresh);
  cacheable.headers.set("Cache-Control", `public, max-age=${STATS_CACHE_TTL_SECONDS}`);
  // Store the clean response (no cache-status header), then mark the one we serve
  // as a miss. A later hit is served from the stored copy and marked hit.
  ctx.waitUntil(cache.put(cacheKey, cacheable.clone()));
  cacheable.headers.set("X-Ctx-Cache", "miss");
  return cacheable;
}

async function readStats(env: Env): Promise<Response> {
  const [totals, countries, programs, countryPrograms, agents, agentInstalls, versions, versionPrograms] = await Promise.all([
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
    env.ctx_wire_telemetry
      .prepare(
        `SELECT version, commands, raw_bytes, emitted_bytes, bytes_saved, tokens_saved, reports, updated_at
         FROM version_stats
         ORDER BY updated_at DESC
         LIMIT 100`,
      )
      .all(),
    env.ctx_wire_telemetry
      .prepare(
        `SELECT version, program, runs, raw_bytes, emitted_bytes, bytes_saved, tokens_saved, updated_at
         FROM version_program_stats
         ORDER BY version DESC, runs DESC
         LIMIT 2000`,
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
    versions: versions.results,
    version_programs: versionPrograms.results,
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

export interface Bucket {
  name: string;
  runs: number;
  bytesSaved: number;
  rawBytes: number;
  emittedBytes: number;
  tokensSaved: number;
}

interface ImpactTotals {
  commands: number;
  rawBytes: number;
  emittedBytes: number;
  bytesSaved: number;
  tokensSaved: number;
}

// sanitizeImpact validates an impact payload into consistent, bounded totals
// and breakdowns. Beyond per-field ceilings it enforces the bounds a genuine
// client actually respects: saved bytes never exceed raw bytes, claimed tokens
// never exceed what the saved volume can encode, and a breakdown never sums
// past its totals. A crafted POST that violates them is clamped or stripped
// rather than rejected, so the volume it is entitled to still counts while the
// excess cannot inflate the public stats. Each guard that fired is named in
// flags for logging.
//
// NOTE: saved is bounded by raw, NOT by raw - emitted. The client floors each
// command's saved to 0 when a synthetic on_empty message makes emitted exceed
// raw (internal/gain gain.go: `saved := rawBytes - emittedBytes; if saved < 0
// { saved = 0 }`), so the summed saved legitimately exceeds summed raw minus
// summed emitted for any client that runs empty searches. raw is the only
// valid ceiling: each per-command term is max(0, raw - emitted) <= raw, so the
// sum is <= sum(raw). An earlier raw-minus-emitted bound wrongly flagged (and,
// for on_empty-heavy users, zeroed) genuine savings.
export function sanitizeImpact(payload: TelemetryPayload): ImpactTotals & {
  programs: Bucket[];
  agents: Bucket[];
  flags: string[];
} {
  const flags: string[] = [];
  const commands = clampInt(payload.commands, 0, MAX_COMMANDS);
  const rawBytes = clampInt(payload.raw_bytes, 0, MAX_RAW_BYTES);
  // Emitted is capped at raw for a tidy display (it can legitimately exceed raw
  // on on_empty-heavy reports); this cap is cosmetic and predates the guards.
  const emittedBytes = Math.min(clampInt(payload.emitted_bytes, 0, MAX_EMITTED_BYTES), rawBytes);
  // Saved is bounded by raw (see NOTE): a crafted report cannot claim it saved
  // more than the bytes it produced, but on_empty savings are never clipped.
  const bytesSaved = Math.min(clampInt(payload.bytes_saved, 0, MAX_BYTES_SAVED), rawBytes);
  const claimedTokens = clampInt(payload.tokens_saved, 0, MAX_TOKENS_SAVED);
  const tokensSaved = Math.min(claimedTokens, maxTokensFor(bytesSaved));
  if (tokensSaved < claimedTokens) {
    flags.push("tokens_saved_over_ratio");
  }

  const totals = { commands, rawBytes, emittedBytes, bytesSaved, tokensSaved };
  let programs = parsePrograms(payload.programs);
  if (!bucketsWithinTotals(programs, totals)) {
    programs = [];
    flags.push("programs_over_totals");
  }
  let agents = parseAgents(payload.agents);
  if (!bucketsWithinTotals(agents, totals)) {
    agents = [];
    flags.push("agents_over_totals");
  }
  return { ...totals, programs, agents, flags };
}

// maxTokensFor bounds a claimed token count by the byte volume backing it.
// The client derives tokens as ceil(bytes/4) (internal/telemetry
// approxTokens); two bytes per token leaves 2x headroom for any future
// estimator while still rejecting counts no real byte stream could encode.
function maxTokensFor(bytesSaved: number): number {
  return Math.ceil(bytesSaved / 2);
}

// bucketsWithinTotals reports whether a breakdown stays inside its report's
// totals on every axis. A genuine client's breakdown sums to at most the
// totals (top-N truncation only lowers it; each command has at most one
// program and one agent, so runs compare against commands). Tokens get one
// count of slack per entry because the client rounds each bucket's token
// estimate up independently.
function bucketsWithinTotals(buckets: Bucket[], totals: ImpactTotals): boolean {
  let runs = 0;
  let rawBytes = 0;
  let emittedBytes = 0;
  let bytesSaved = 0;
  let tokensSaved = 0;
  for (const b of buckets) {
    runs += b.runs;
    rawBytes += b.rawBytes;
    emittedBytes += b.emittedBytes;
    bytesSaved += b.bytesSaved;
    tokensSaved += b.tokensSaved;
  }
  return (
    runs <= totals.commands &&
    rawBytes <= totals.rawBytes &&
    emittedBytes <= totals.emittedBytes &&
    bytesSaved <= totals.bytesSaved &&
    tokensSaved <= totals.tokensSaved + buckets.length
  );
}

function parsePrograms(value: unknown): Bucket[] {
  return parseBuckets(value, MAX_PROGRAMS);
}

function parseAgents(value: unknown): Bucket[] {
  return parseBuckets(value, MAX_AGENTS, KNOWN_AGENTS);
}

// parseBuckets validates a {name: count|object} map (programs or agents) into a
// capped, sanitized list. The same name regex and counter clamps apply to both,
// so an agent breakdown is validated exactly like a program breakdown. When an
// allowlist is given, any name not on it folds into the "other" bucket (with its
// counts merged), so a direct POST cannot inject an arbitrary label and the row
// count stays bounded to the known set.
function parseBuckets(value: unknown, maxEntries: number, allowlist?: Set<string>): Bucket[] {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return [];
  }

  const merged = new Map<string, Bucket>();

  const add = (name: string, delta: Omit<Bucket, "name">) => {
    const cur = merged.get(name) ?? {
      name,
      runs: 0,
      rawBytes: 0,
      emittedBytes: 0,
      bytesSaved: 0,
      tokensSaved: 0,
    };
    // Re-clamp each accumulator so merging several folded entries in one payload
    // cannot exceed the per-report ceiling.
    cur.runs = clampInt(cur.runs + delta.runs, 0, MAX_PROGRAM_RUNS);
    cur.rawBytes = clampInt(cur.rawBytes + delta.rawBytes, 0, MAX_RAW_BYTES);
    cur.emittedBytes = clampInt(cur.emittedBytes + delta.emittedBytes, 0, MAX_EMITTED_BYTES);
    cur.bytesSaved = clampInt(cur.bytesSaved + delta.bytesSaved, 0, MAX_BYTES_SAVED);
    cur.tokensSaved = clampInt(cur.tokensSaved + delta.tokensSaved, 0, MAX_TOKENS_SAVED);
    // Re-enforce the bounds after merging (saved by raw, tokens by saved); the
    // on_empty note in sanitizeImpact applies equally per bucket.
    cur.emittedBytes = Math.min(cur.emittedBytes, cur.rawBytes);
    cur.bytesSaved = Math.min(cur.bytesSaved, cur.rawBytes);
    cur.tokensSaved = Math.min(cur.tokensSaved, maxTokensFor(cur.bytesSaved));
    merged.set(name, cur);
  };

  for (const [rawName, rawValue] of Object.entries(value as Record<string, ProgramValue>)) {
    const raw = rawName.trim().toLowerCase();
    if (!PROGRAM_RE.test(raw)) {
      continue;
    }
    const name = allowlist && !allowlist.has(raw) ? OTHER_BUCKET : raw;
    // Cap distinct names; a folded "other" that already exists does not count
    // against the cap, so real known entries still land after a burst of junk.
    if (!merged.has(name) && merged.size >= maxEntries) {
      continue;
    }

    if (typeof rawValue === "number") {
      const runs = clampInt(rawValue, 0, MAX_PROGRAM_RUNS);
      if (runs > 0) {
        add(name, { runs, rawBytes: 0, emittedBytes: 0, bytesSaved: 0, tokensSaved: 0 });
      }
      continue;
    }

    if (!rawValue || typeof rawValue !== "object" || Array.isArray(rawValue)) {
      continue;
    }

    const runs = clampInt(rawValue.runs ?? rawValue.count, 0, MAX_PROGRAM_RUNS);
    // Each entry is bounded like the report totals: saved by its own raw (not
    // raw - emitted; see the on_empty note in sanitizeImpact), tokens by saved.
    const rawBytes = clampInt(rawValue.raw_bytes, 0, MAX_RAW_BYTES);
    const emittedBytes = Math.min(clampInt(rawValue.emitted_bytes, 0, MAX_EMITTED_BYTES), rawBytes);
    const bytesSaved = Math.min(clampInt(rawValue.bytes_saved, 0, MAX_BYTES_SAVED), rawBytes);
    const tokensSaved = Math.min(clampInt(rawValue.tokens_saved, 0, MAX_TOKENS_SAVED), maxTokensFor(bytesSaved));
    if (runs > 0 || rawBytes > 0 || emittedBytes > 0 || bytesSaved > 0 || tokensSaved > 0) {
      add(name, { runs, rawBytes, emittedBytes, bytesSaved, tokensSaved });
    }
  }

  return [...merged.values()];
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
