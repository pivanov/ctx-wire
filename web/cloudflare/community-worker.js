// GET /v1/community: { stars, stargazers } for STARGAZER_REPO (env var, default
// pivanov/ctx-wire), cached ~1h with a stale fallback, no GitHub token needed.
// Merge into the telemetry worker: route /v1/community to handleCommunity().

const DEFAULT_REPO = "pivanov/ctx-wire";
const FRESH_TTL = 3600; // serve cached for 1 hour
const STALE_TTL = 604800; // keep last-good up to 7 days as a fallback
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
    const payload = await fetchCommunity(repo);
    const body = JSON.stringify(payload);
    ctx.waitUntil(cache.put(freshKey, jsonResponse(body, FRESH_TTL)));
    ctx.waitUntil(cache.put(staleKey, jsonResponse(body, STALE_TTL)));
    return withCors(jsonResponse(body, FRESH_TTL));
  } catch (err) {
    // 3) GitHub failed (e.g. 403 rate limit) → serve last-good if we have it
    const stale = await cache.match(staleKey);
    if (stale) return withCors(stale);
    return withCors(
      jsonResponse(
        JSON.stringify({ stars: 0, stargazers: [], error: String(err) }),
        60
      )
    );
  }
}

async function fetchCommunity(repo) {
  const headers = { "User-Agent": UA, Accept: "application/vnd.github+json" };
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
