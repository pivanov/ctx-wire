import { motion, type Transition, useReducedMotion } from "motion/react";
import { useEffect, useMemo, useState } from "react";
import { topPrograms } from "../format";
import { fadeUp } from "../lib/variants";
import type { TImpactStats, TProgramStats } from "../types";
import { SectionKicker } from "./section-heading";

// "Real cuts": a live rotation through the commands ctx-wire compresses.
//
// What's REAL: the command list, the token numbers, the run counts, and the
// compression % all come from telemetry (topPrograms, per-run averages).
// What's ILLUSTRATIVE: the dropped/kept output lines. ctx-wire never collects
// command output (the full log stays on your disk), so the snippets show the
// *kind* of noise a command emits, matched by program name. The snippets carry
// NO hard counts, so nothing can contradict the real token panel beside them.

type TSnippet = {
  cmd: string;
  dropped: string[]; // a few example noise lines (no totals)
  kept: string[]; // the kind of signal that survives (no totals)
};

type TCut = TSnippet & {
  raw: number; // real per-run average tokens the shell printed
  sent: number; // real per-run average tokens the agent receives
  runs: number; // real number of runs the average is taken over
};

// Illustrative output per program, keyed by telemetry program name. Counts are
// deliberately absent: the only numbers shown come from the real token panel.
const SNIPPETS: Record<string, TSnippet> = {
  cargo: {
    cmd: "cargo build",
    dropped: [
      "Compiling serde v1.0.197",
      "Compiling tokio v1.36.0",
      "warning: unused import: `std::fmt::Display`",
    ],
    kept: [
      "error[E0599]: no method `poll_read` on `TcpStream`",
      "   --> src/net.rs:88:14",
    ],
  },
  rg: {
    cmd: 'rg "TODO" .',
    dropped: [
      "app/page.tsx:42:   // TODO: handle the error path",
      "lib/db.ts:88:     // TODO: cache the lookup",
      "api/users.ts:13:  // TODO: paginate results",
    ],
    kept: ["→ matches grouped by file", "→ the rest stays on disk"],
  },
  grep: {
    cmd: "grep -rn useEffect src",
    dropped: [
      "src/app.tsx:12:    useEffect(() => {",
      "src/list.tsx:30:   useEffect(() => {",
      "src/feed.tsx:8:    useEffect(() => {",
    ],
    kept: ["→ matches grouped by file", "→ bodies stay on disk"],
  },
  git: {
    cmd: "git status",
    dropped: [
      "modified:   src/app.tsx",
      "modified:   src/api/users.ts",
      "untracked:  notes/scratch.md",
    ],
    kept: [
      "→ branch and ahead/behind",
      "→ staged · modified · untracked, summarized",
    ],
  },
  npm: {
    cmd: "npm install",
    dropped: [
      "npm warn deprecated inflight@1.0.6",
      "npm fund: packages looking for funding",
      "added a package, audited the tree",
    ],
    kept: ["→ what was added", "→ warnings and vulnerabilities"],
  },
  pnpm: {
    cmd: "pnpm install",
    dropped: [
      "Progress: resolved, reused, downloaded",
      "packages/app: + dependencies",
      "node_modules/.pnpm linked",
    ],
    kept: ["→ done, with timing", "→ peer warnings, if any"],
  },
  yarn: {
    cmd: "yarn install",
    dropped: [
      "[1/4] Resolving packages",
      "[2/4] Fetching packages",
      "[3/4] Linking dependencies",
    ],
    kept: ["→ success, lockfile saved", "→ done, with timing"],
  },
  go: {
    cmd: "go test ./...",
    dropped: [
      "ok   ctx-wire/internal/run    0.02s",
      "ok   ctx-wire/internal/hook   0.11s",
      "ok   ctx-wire/internal/agent  0.03s",
    ],
    kept: ["FAIL ctx-wire/internal/telemetry", "--- FAIL: TestFlush (0.00s)"],
  },
  docker: {
    cmd: "docker build .",
    dropped: [
      "#5 [2/8] RUN apt-get update",
      "#6 [3/8] COPY . /app",
      "#7 [4/8] RUN go build -o bin",
    ],
    kept: ["=> => writing image sha256:9f2c…", "=> => naming to app:latest"],
  },
  bun: {
    cmd: "bun install",
    dropped: ["+ react@19.0.0", "+ vite@6.0.3", "+ @types/node@22.10.2"],
    kept: ["→ installed, with timing", "→ lockfile up to date"],
  },
  bunx: {
    cmd: "bunx prettier --write .",
    dropped: [
      "formatted  src/app.tsx",
      "formatted  src/api/users.ts",
      "formatted  src/components/feed.tsx",
    ],
    kept: ["→ formatted, with timing", "→ file list collapsed"],
  },
  node: {
    cmd: "node scripts/seed.mjs",
    dropped: ["seeded user 1001", "seeded user 1002", "seeded user 1003"],
    kept: ["→ rows seeded, with timing", "→ done"],
  },
  kubectl: {
    cmd: "kubectl get pods",
    dropped: [
      "api-7c9f      1/1   Running",
      "web-5d2a      1/1   Running",
      "worker-1a3b   1/1   Running",
    ],
    kept: ["→ the pods that aren't healthy", "→ restarts worth noticing"],
  },
  base64: {
    cmd: "base64 cert.der",
    dropped: [
      "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0t",
      "MIIDdzCCAl+gAwIBAgIEbACz5DANBgkq",
      "hkiG9w0BAQsFADBpMQswCQYDVQQGEwJV",
    ],
    kept: ["→ the blob, collapsed to a marker", "→ full data stays on disk"],
  },
  cat: {
    cmd: "cat package-lock.json",
    dropped: [
      '    "node_modules/react": {',
      '      "version": "19.0.0",',
      '      "resolved": "https://registry.npmjs.org/…"',
    ],
    kept: ["→ head + tail kept", "→ the middle stays on disk"],
  },
  tr: {
    cmd: "cat access.log | tr -s ' '",
    dropped: [
      "127.0.0.1 GET /api/users 200",
      "127.0.0.1 GET /assets/app.js 304",
      "10.0.0.4 POST /auth/login 401",
    ],
    kept: ["→ the normalized stream", "→ collapsed to a summary"],
  },
  nl: {
    cmd: "nl server.log",
    dropped: [
      "     1  starting server on :8080",
      "     2  connected to postgres",
      "     3  GET /health 200",
    ],
    kept: ["→ the numbered body", "→ collapsed, full log on disk"],
  },
  sed: {
    cmd: "sed 's/secret/•••/g' env.dump",
    dropped: [
      "DATABASE_URL=postgres://•••",
      "API_KEY=•••",
      "REDIS_URL=redis://•••",
    ],
    kept: ["→ the transformed output", "→ collapsed to a summary"],
  },
  sort: {
    cmd: "sort -u routes.txt",
    dropped: ["/api/auth", "/api/billing", "/api/users"],
    kept: ["→ the unique values", "→ sorted, collapsed"],
  },
  head: {
    cmd: "head -n 5000 dump.sql",
    dropped: [
      "INSERT INTO users VALUES (1, …)",
      "INSERT INTO users VALUES (2, …)",
      "INSERT INTO users VALUES (3, …)",
    ],
    kept: ["→ the head you asked for", "→ capped, full file on disk"],
  },
};

// Filters we actually shipped, framed as a changelog (not the rotation above).
// Only released fixes belong here, never roadmap.
const SHIPPED = [
  { version: "0.1.38", cmd: "uv" },
  { version: "0.1.38", cmd: "bun" },
  { version: "0.1.36", cmd: "rubocop" },
];

// Shown when telemetry has no matching programs yet (idle tab / first paint).
// Seeded with the real top programs + real per-run averages and run counts, so
// the idle state mirrors production rather than inventing different commands.
const FALLBACK: TCut[] = [
  { ...SNIPPETS.rg, raw: 178000, sent: 904, runs: 19472 },
  { ...SNIPPETS.git, raw: 3712, sent: 110, runs: 182954 },
  { ...SNIPPETS.base64, raw: 8011, sent: 1137, runs: 40092 },
  { ...SNIPPETS.grep, raw: 5304, sent: 164, runs: 44443 },
  { ...SNIPPETS.cat, raw: 747, sent: 330, runs: 166949 },
];

const ROTATE_MS = 3800;
const EASE_OUT = [0.23, 1, 0.32, 1] as const;

// A cut is never 100% while the agent still receives tokens. Cap at 99 so we
// never claim we erased output we actually sent.
const pctCut = (c: TCut) =>
  c.sent <= 0 ? 100 : Math.min(99, Math.round((1 - c.sent / c.raw) * 100));

// Real per-run-average token counts for a program, matched to its snippet.
const toCut = (snippet: TSnippet, p: TProgramStats): TCut | null => {
  const raw = Number(p.raw_bytes || 0);
  const saved = Number(p.bytes_saved || 0);
  const runs = Number(p.runs ?? p.count ?? 0);
  if (raw <= 0 || saved <= 0 || runs <= 0) {
    return null;
  }
  const emitted = Number(p.emitted_bytes ?? Math.max(0, raw - saved));
  const rawTok = Math.max(1, Math.round(raw / runs / 4));
  const sentTok = Math.max(1, Math.round(emitted / runs / 4));
  // Need a visible, non-trivial cut to be worth showcasing.
  if (rawTok < 40 || rawTok <= sentTok) {
    return null;
  }
  return { ...snippet, raw: rawTok, sent: sentTok, runs };
};

const buildCuts = (stats: TImpactStats): TCut[] => {
  const real = topPrograms(stats)
    .map((p) => {
      const snippet = SNIPPETS[p.program];
      return snippet ? toCut(snippet, p) : null;
    })
    .filter((c): c is TCut => c !== null)
    .slice(0, 6);
  return real.length >= 3 ? real : FALLBACK;
};

const Bar = ({
  label,
  tokens,
  pct,
  tone,
  delay,
  reduce,
}: {
  label: string;
  tokens: string;
  pct: number;
  tone: "raw" | "sent";
  delay: number;
  reduce: boolean;
}) => {
  const grow: Transition = reduce
    ? { duration: 0 }
    : { duration: 0.7, ease: EASE_OUT, delay };
  return (
    <div className="flex items-center gap-3">
      <span className="w-16 shrink-0 text-2xs text-label">{label}</span>
      <div className="relative h-2.5 flex-1 overflow-hidden rounded-full bg-white/4 ring-1 ring-inset ring-line-soft">
        <motion.div
          className={`h-full rounded-full ${
            tone === "sent" ? "bg-green shadow-marker" : "bg-cyan/30"
          }`}
          style={{
            width: `${Math.max(pct, 2)}%`,
            minWidth: tone === "sent" ? 8 : undefined,
            transformOrigin: "left",
          }}
          initial={reduce ? false : { scaleX: 0 }}
          animate={{ scaleX: 1 }}
          transition={grow}
        />
      </div>
      <span className="w-16 shrink-0 text-right font-bold tabular-nums text-fg">
        {tokens}
      </span>
    </div>
  );
};

const Frame = ({ cut, reduce }: { cut: TCut; reduce: boolean }) => {
  const pct = pctCut(cut);
  return (
    <div className="grid gap-5 lg:grid-cols-[1fr_300px] lg:items-start">
      <div className="min-w-0">
        <div className="mb-3">
          <span className="select-none text-green">$</span> {cut.cmd}
        </div>
        <div className="relative mb-2 pl-3.5">
          <span className="absolute inset-y-1 left-0 w-px bg-line-soft" />
          <div className="mb-1 inline-flex items-center gap-1.5 text-2xs uppercase tracking-caps text-label">
            <span className="text-dim">✕</span> dropped
          </div>
          {cut.dropped.map((line) => (
            <div key={line} className="truncate leading-relaxed text-label">
              {line}
            </div>
          ))}
          <div className="mt-0.5 text-2xs text-label">
            ⋮ the rest, dropped
            <span className="text-dim"> · full log on disk</span>
          </div>
        </div>
        <div className="rounded-md border-l-2 border-green bg-green/4 py-2.5 pl-3.5 pr-3">
          <div className="mb-1.5 text-2xs uppercase tracking-caps text-green">
            kept · what your agent reads
          </div>
          {cut.kept.map((line) => (
            <div
              key={line}
              className="whitespace-pre-wrap leading-relaxed text-fg"
            >
              {line}
            </div>
          ))}
        </div>
      </div>

      <div className="rounded-panel bg-white/2 p-4 ring-1 ring-inset ring-line-soft">
        <div className="mb-3 flex items-baseline gap-2">
          <span className="font-display text-5xl font-extrabold leading-none text-green">
            −{pct}%
          </span>
          <span className="text-2xs text-label">fewer tokens</span>
        </div>
        <div className="space-y-2.5">
          <Bar
            label="shell"
            tokens={cut.raw.toLocaleString()}
            pct={100}
            tone="raw"
            delay={0.05}
            reduce={reduce}
          />
          <Bar
            label="agent"
            tokens={cut.sent.toLocaleString()}
            pct={(cut.sent / cut.raw) * 100}
            tone="sent"
            delay={0.28}
            reduce={reduce}
          />
        </div>
        <p className="m-0 mt-3 text-2xs leading-relaxed text-dim">
          Average across {cut.runs.toLocaleString()} real runs.
        </p>
      </div>
    </div>
  );
};

const CutsTerminal = ({ cuts }: { cuts: TCut[] }) => {
  const reduce = Boolean(useReducedMotion());
  const [idx, setIdx] = useState(0);
  const [paused, setPaused] = useState(false);
  const cut = cuts[idx % cuts.length];

  useEffect(() => {
    if (reduce || paused || cuts.length <= 1) {
      return;
    }
    const id = window.setInterval(
      () => setIdx((i) => (i + 1) % cuts.length),
      ROTATE_MS
    );
    return () => window.clearInterval(id);
  }, [paused, reduce, cuts.length]);

  return (
    <motion.div
      variants={reduce ? undefined : fadeUp}
      initial={reduce ? undefined : "hidden"}
      whileInView="visible"
      viewport={{ once: true, amount: 0.1 }}
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
      className="window-shadow w-full overflow-hidden rounded-window bg-screen"
    >
      <div className="titlebar-bg relative flex h-8 items-center px-3.5">
        <div className="flex items-center gap-2">
          <span className="size-2.5 rounded-full bg-mac-close ring-1 ring-inset ring-black/15" />
          <span className="size-2.5 rounded-full bg-mac-min ring-1 ring-inset ring-black/15" />
          <span className="size-2.5 rounded-full bg-mac-zoom ring-1 ring-inset ring-black/15" />
        </div>
        <div className="pointer-events-none absolute inset-0 flex items-center justify-center gap-2 font-mono text-2xs text-chrome">
          <span className="text-green">›_</span>
          ctx-wire run
        </div>
      </div>

      <div className="screen">
        <div className="scan" aria-hidden="true" />
        <div className="glare" aria-hidden="true" />
        <div className="relative z-10 w-full font-mono text-term text-fg">
          <div className="min-h-[260px]">
            <motion.div
              key={cut.cmd}
              initial={reduce ? false : { opacity: 0, y: 10 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.32, ease: EASE_OUT }}
            >
              <Frame cut={cut} reduce={reduce} />
            </motion.div>
          </div>

          <div className="mt-4 flex items-center justify-center gap-1.5">
            {cuts.map((c, i) => (
              <button
                type="button"
                key={c.cmd}
                aria-label={c.cmd}
                onClick={() => setIdx(i)}
                className={`h-1.5 rounded-full transition-all duration-200 ease-out ${
                  i === idx % cuts.length
                    ? "w-6 bg-green"
                    : "w-1.5 bg-dim hover:bg-label"
                }`}
              />
            ))}
          </div>

          {/* changelog: recent filter fixes (separate from the rotation) */}
          <div className="mt-5 flex flex-wrap items-center gap-x-3 gap-y-2 border-t border-line-soft pt-4 text-2xs">
            <span className="text-label">Newest filter fixes:</span>
            {SHIPPED.map((s) => (
              <span
                key={s.cmd}
                className="inline-flex items-center gap-2 rounded-full bg-green/10 px-3 py-1 ring-1 ring-inset ring-green/25"
              >
                <span className="text-green">{s.version}</span>
                <span className="font-bold text-fg">{s.cmd}</span>
              </span>
            ))}
            <span className="text-dim">· sharper every release</span>
          </div>
        </div>
      </div>
    </motion.div>
  );
};

export const CommandCuts = ({ stats }: { stats: TImpactStats }) => {
  const cuts = useMemo(() => buildCuts(stats), [stats]);
  return (
    <section className="flex w-full max-w-term flex-col gap-4">
      <SectionKicker desc="One command at a time: the noise dropped, the tokens saved.">
        real cuts
      </SectionKicker>
      <h2 className="m-0 mb-1 font-display text-reach font-extrabold text-head">
        Watch it <span className="text-green">cut</span>.
      </h2>
      <CutsTerminal cuts={cuts} />
    </section>
  );
};
