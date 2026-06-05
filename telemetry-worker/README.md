# ctx-wire telemetry Worker

Cloudflare Worker + D1 backend for public aggregate ctx-wire impact stats.

It stores only:

- country-level reported installs (successful `ctx-wire init <agent>` runs)
- country-level command totals, raw bytes, emitted bytes, bytes saved, and
  estimated tokens saved
- program-level run counts, raw bytes, emitted bytes, saved bytes, and estimated
  saved tokens
- agent-level run counts and saved bytes/tokens (claude, codex, ...): the agent
  type is a category, not an identity, so it stays anonymous and aggregate

It does not store commands, arguments, paths, raw output, repo names, usernames,
hostnames, install IDs, or IP addresses. Country is derived server-side from
Cloudflare's `request.cf.country`. Dollar figures are never stored: the payload
is token-only, and any pricing is a website-side estimate.

## Setup

Login:

```sh
npx wrangler login
```

The D1 database has already been created:

```text
ctx-wire-telemetry
10035374-0167-442f-8ca8-e1ce3a934312
```

Apply the schema:

```sh
cd telemetry-worker
npm run schema:remote
```

Deploy:

```sh
npm run deploy
```

Wrangler will print the public `workers.dev` URL.

## Endpoints

```text
POST /v1/telemetry
POST /v1/impact
GET  /v1/impact
GET  /v1/stats
GET  /health
```

`POST /v1/telemetry` and `POST /v1/impact` are aliases.

## Abuse protection

This is an anonymous, keyless public endpoint, so it cannot truly verify "only
the CLI" (a token baked into the open-source binary is extractable and is not
real auth). Protection is therefore about bounding blast radius, not identity:

- **Per-IP rate limit** on writes (`WRITE_RATE_LIMITER`, 60 req/min/IP by
  default); over-limit requests get `429 rate_limited`. Tune `limit` in
  `wrangler.toml` if shared-NAT traffic trips it.
- **Value clamps** on every numeric field and a strict name regex on
  program/agent keys, so a single report can only move aggregates within sane
  bounds.
- `schema` and `event` are validated; oversized bodies get `413`.
- Cloudflare's edge DDoS/bot protection handles the crude floods.

The data is directional public stats, not billing, so this is a deliberate
"bound the damage" posture rather than strong integrity.

Install event:

```json
{
  "schema": 1,
  "event": "install",
  "agent": "claude",
  "machine": true
}
```

Modern clients send an install event for every successful `ctx-wire init
<agent>` run, including repeats for the same agent. `machine: true` increments
the aggregate reported-install counter; `agent` increments the per-agent install
counter. Legacy install events without `agent` or `machine` still count as
country-level installs only.

Impact event:

```json
{
  "schema": 1,
  "event": "impact",
  "commands": 123,
  "raw_bytes": 456789,
  "emitted_bytes": 333333,
  "bytes_saved": 123456,
  "tokens_saved": 32100,
  "programs": {
    "cat": {
      "count": 42,
      "raw_bytes": 300000,
      "emitted_bytes": 120000,
      "bytes_saved": 180000,
      "tokens_saved": 45000
    },
    "rg": { "runs": 18, "bytes_saved": 10000, "tokens_saved": 2500 }
  },
  "agents": {
    "claude": {
      "count": 80,
      "raw_bytes": 320000,
      "emitted_bytes": 130000,
      "bytes_saved": 190000,
      "tokens_saved": 47500
    },
    "codex": { "count": 33, "bytes_saved": 60000, "tokens_saved": 15000 }
  }
}
```

`programs` and `agents` use the same shape: a name maps to a run count or an
object with `count` or `runs`, plus optional `raw_bytes`, `emitted_bytes`,
`bytes_saved`, and `tokens_saved`. `agents` is the per-invoking-agent breakdown;
unattributed commands are omitted, so the "unattributed" share is
`commands - sum(agents.count)`. `GET /v1/stats` returns an `agents` array
alongside `programs`.
