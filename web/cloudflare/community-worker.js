// GET /v1/community: { stars, stargazers } for STARGAZER_REPO (env var, default
// pivanov/ctx-wire), cached with a stale fallback, no GitHub token needed.
// Merge into the telemetry worker: route /v1/community to handleCommunity().

const DEFAULT_REPO = "pivanov/ctx-wire";
const FRESH_TTL = 21600; // serve a good value for 6h before refreshing (stars move slowly)
const RETRY_TTL = 300; // after a failed refresh, wait 5m before hitting GitHub again
const STALE_TTL = 604800; // keep last-good up to 7 days as the fallback body
const GH = "https://api.github.com";
const UA = "ctx-wire-web (+https://github.com/pivanov/ctx-wire)";

const CORS = {
  "Access-Control-Allow-Origin": "*",
  "Access-Control-Allow-Methods": "GET, OPTIONS",
};

export async function handleCommunity(request, env, ctx) {
  if (request.method === "OPTIONS") {
    return new Response(null, { headers: CORS });
  }

  const repo = (env && env.STARGAZER_REPO) || DEFAULT_REPO;
  const cache = caches.default;
  const freshKey = new Request(`https://cache.ctxwire/community/${repo}/fresh`);
  const staleKey = new Request(`https://cache.ctxwire/community/${repo}/stale`);

  // 1) fresh cache hit (< 1h old)
  const fresh = await cache.match(freshKey);
  if (fresh) return withCors(fresh);

  // 2) cache miss → fetch GitHub
  try {
    const payload = await fetchCommunity(repo, env);
    const body = JSON.stringify(payload);
    ctx.waitUntil(cache.put(freshKey, jsonResponse(body, FRESH_TTL)));
    ctx.waitUntil(cache.put(staleKey, jsonResponse(body, STALE_TTL)));
    return withCors(jsonResponse(body, FRESH_TTL));
  } catch (err) {
    // 3) GitHub failed (typically the 60/hr unauthenticated limit on the shared
    // Worker IP). Serve last-good AND re-arm both caches so a failure can't
    // snowball: write it back into the fresh key with a short TTL so we retry
    // GitHub at most once per RETRY_TTL instead of on every request (hammering
    // it would self-inflict the rate limit), and re-put the stale key so the
    // last-good never decays to the stars:0 fallback.
    console.log("community: github refresh failed", repo, String(err));
    const stale = await cache.match(staleKey);
    if (stale) {
      const body = await stale.text();
      ctx.waitUntil(cache.put(freshKey, jsonResponse(body, RETRY_TTL)));
      ctx.waitUntil(cache.put(staleKey, jsonResponse(body, STALE_TTL)));
      return withCors(jsonResponse(body, RETRY_TTL));
    }
    // No last-good at all (cold worker that has never succeeded): negative-cache
    // briefly so we still retry soon without hammering.
    const body = JSON.stringify({ stars: 0, stargazers: [], error: String(err) });
    ctx.waitUntil(cache.put(freshKey, jsonResponse(body, RETRY_TTL)));
    return withCors(jsonResponse(body, RETRY_TTL));
  }
}

async function fetchCommunity(repo, env) {
  const headers = { "User-Agent": UA, Accept: "application/vnd.github+json" };
  // Optional GITHUB_TOKEN secret. Public repo data needs no scope, so a
  // read-only/public-only token just lifts the rate limit from 60/hr
  // (unauthenticated, shared Worker IP) to 5,000/hr. Absent -> unauthenticated,
  // and the cache/backoff above degrades gracefully.
  const token = env && env.GITHUB_TOKEN;
  if (token) headers.Authorization = `Bearer ${token}`;
  const [repoRes, starRes] = await Promise.all([
    fetch(`${GH}/repos/${repo}`, { headers }),
    fetch(`${GH}/repos/${repo}/stargazers?per_page=100`, { headers }),
  ]);
  if (!repoRes.ok) throw new Error(`repo ${repoRes.status}`);
  if (!starRes.ok) throw new Error(`stargazers ${starRes.status}`);

  const meta = await repoRes.json();
  const list = await starRes.json();
  const stargazers = (Array.isArray(list) ? list : []).map((u) => ({
    login: u.login,
    avatar: u.avatar_url,
    url: u.html_url,
  }));
  return {
    stars: Number(meta.stargazers_count || stargazers.length || 0),
    stargazers,
    cached_at: new Date().toISOString(),
  };
}

function jsonResponse(body, ttl) {
  return new Response(body, {
    headers: {
      "Content-Type": "application/json; charset=utf-8",
      "Cache-Control": `s-maxage=${ttl}`,
    },
  });
}

function withCors(res) {
  const out = new Response(res.body, res);
  for (const [k, v] of Object.entries(CORS)) out.headers.set(k, v);
  return out;
}

// Standalone deploy (option B): this worker answers /v1/community and 404s else.
export default {
  async fetch(request, env, ctx) {
    const url = new URL(request.url);
    if (url.pathname === "/v1/community") {
      return handleCommunity(request, env, ctx);
    }
    return new Response("Not found", { status: 404, headers: CORS });
  },
};
