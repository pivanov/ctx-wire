// GET /v1/community: { stars, stargazers } for STARGAZER_REPO (env var, default
// pivanov/ctx-wire), cached with a stale fallback. The star COUNT needs no
// token (the repo endpoint is public). The stargazer AVATAR list DOES: as of
// 2026-08-07 GitHub returns 401 for /stargazers on every public repo when
// unauthenticated, so without a valid GITHUB_TOKEN that list is always [].
// Merge into the telemetry worker: route /v1/community to handleCommunity().

const DEFAULT_REPO = "pivanov/ctx-wire";
// CACHE_VERSION namespaces the cache keys. The keys are synthetic internal URLs,
// not real hostnames, so a Cloudflare dashboard purge cannot reach them and a
// query param cannot bust them (the key ignores the request URL). Bumping this
// is therefore the only way to force a refresh before FRESH_TTL expires: do it
// whenever a change alters the SHAPE of the payload or fixes a bug whose bad
// result is already cached, otherwise a 6h-old wrong answer keeps being served.
//
// NEVER bump it while GitHub is failing. The keys namespace the stale entry too,
// so a bump throws away the last-good payload that the error path serves as a
// fallback. That happened on 2026-08-07: a bad token broke the refresh, the bump
// discarded the cached "77 stars", and the site dropped to zero instead of
// riding out the outage on stale data. Bump only once upstream is healthy.
const CACHE_VERSION = "v6";

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
  const freshKey = new Request(`https://cache.ctxwire/community/${CACHE_VERSION}/${repo}/fresh`);
  const staleKey = new Request(`https://cache.ctxwire/community/${CACHE_VERSION}/${repo}/stale`);

  // 1) fresh cache hit (younger than FRESH_TTL)
  const fresh = await cache.match(freshKey);
  if (fresh) return withCors(fresh);

  // 2) cache miss → fetch GitHub
  try {
    const payload = await fetchCommunity(repo, env, cache, staleKey);
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

async function fetchCommunity(repo, env, cache, staleKey) {
  const headers = { "User-Agent": UA, Accept: "application/vnd.github+json" };
  // Optional GITHUB_TOKEN secret. It does two things: lifts the rate limit from
  // 60/hr (unauthenticated, shared Worker IP) to 5,000/hr, and is REQUIRED for
  // the stargazer avatars below (GraphQL rejects anonymous requests outright).
  // The star count never needs it. Absent -> unauthenticated count, no avatars,
  // and the cache/backoff above degrades gracefully.
  //
  // A classic PAT with NO scopes is sufficient and is the minimum to use here;
  // public repo data needs none. Do not grant contents/write scopes (see the
  // GraphQL note below for why REST tempts you to).
  // Trim the secret: a value pasted into `wrangler secret put` can pick up a
  // trailing newline, and GitHub rejects the whole header if it does.
  const token = ((env && env.GITHUB_TOKEN) || "").trim();
  const authHeaders = token
    ? { ...headers, Authorization: `Bearer ${token}` }
    : headers;

  // The star COUNT is the value that matters and comes from the repo endpoint.
  // It works WITHOUT a token, so a bad token must never be able to break it.
  //
  // This bit us live on 2026-08-07: an invalid secret made this call 401, the
  // throw below collapsed the payload to {stars: 0}, and the site announced
  // "Be the first to star ctx-wire" while the repo had 77 stars. A token is an
  // optimization here (rate limit) and a requirement only for the avatars, so
  // on any auth-shaped failure drop it and retry anonymously rather than fail.
  let repoRes = await fetch(`${GH}/repos/${repo}`, { headers: authHeaders });
  let tokenRejected = false;
  if (token && (repoRes.status === 401 || repoRes.status === 403)) {
    tokenRejected = true;
    repoRes = await fetch(`${GH}/repos/${repo}`, { headers });
  }
  // Only a genuine failure of the anonymous path is fatal: then throw so the
  // caller serves stale and backs off.
  if (!repoRes.ok) throw new Error(`repo ${repoRes.status}`);
  const meta = await repoRes.json();
  const stars = Number(meta.stargazers_count || 0);

  // The stargazer AVATAR list is best-effort and must NOT gate the count.
  //
  // Use GraphQL, NOT the REST /stargazers endpoint. Measured 2026-08-07, REST is
  // closed to personal access tokens in every form we tried:
  //
  //   unauthenticated                 401 Requires authentication
  //   fine-grained, metadata=read     403 Resource not accessible
  //   classic, no scopes              404
  //
  // and its x-accepted-github-permissions header asks for `contents=write`,
  // i.e. write access to the source, to read a PUBLIC star list. Never grant
  // that for this. GraphQL returns the same data for the same token, so the
  // whole problem disappears by changing endpoint rather than by escalating
  // permissions.
  //
  // No token means no avatars, and that is FINE: the count above comes from the
  // public REST repo endpoint and never depends on the token. Two earlier
  // versions of this comment were wrong about the cause (one claimed a 404 was
  // expected even with a valid token, one claimed a token would fix REST), which
  // is why the failure codes are written out above rather than summarized.
  //
  // Fall back to the last-good avatars from the stale cache so the row does not
  // blank out while the count stays correct.
  // Skip when the token was already rejected above: it cannot work here either,
  // and retrying just burns a request on a known-bad credential.
  let stargazers = [];
  if (token && !tokenRejected) {
    try {
      const [owner, name] = repo.split("/");
      // Variables, not string interpolation: repo comes from an env var and must
      // not be splicable into the query document.
      const gqlRes = await fetch(`${GH}/graphql`, {
        method: "POST",
        headers: { ...authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({
          query:
            "query($owner:String!,$name:String!){repository(owner:$owner,name:$name)" +
            "{stargazers(first:100){nodes{login avatarUrl url}}}}",
          variables: { owner, name },
        }),
      });
      if (gqlRes.ok) {
        const gql = await gqlRes.json();
        const nodes = gql?.data?.repository?.stargazers?.nodes;
        stargazers = (Array.isArray(nodes) ? nodes : []).map((u) => ({
          login: u.login,
          avatar: u.avatarUrl,
          url: u.url,
        }));
      }
    } catch {
      // ignore: avatars are best-effort
    }
  }
  if (stargazers.length === 0 && cache && staleKey) {
    const prev = await cache.match(staleKey);
    if (prev) {
      try {
        const p = await prev.json();
        if (Array.isArray(p.stargazers)) stargazers = p.stargazers;
      } catch {
        // ignore: no usable last-good list
      }
    }
  }

  return { stars, stargazers, cached_at: new Date().toISOString() };
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
