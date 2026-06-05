import {
  IconCheck,
  IconCopy,
  IconDatabase,
  IconScan,
  IconTerminal2,
} from "@tabler/icons-react";
import { motion, useReducedMotion } from "motion/react";
import { type CSSProperties, useEffect, useState } from "react";
import { formatBytes, savedPct, topPrograms } from "../format";
import { useCopy } from "../hooks/use-copy";
import { fadeUp, scaleIn, staggerContainer } from "../lib/variants";
import type { ImpactStats } from "../types";

const AGENTS = [
  "claude",
  "cursor",
  "codex",
  "gemini",
  "copilot",
  "cline",
  "windsurf",
];

const TRUST = [
  "142 built-in filters",
  "secrets scrubbed",
  "full logs kept on disk",
  "MIT licensed",
];

const GRID_TOTAL = 140;

const label = (id: string) => id.charAt(0).toUpperCase() + id.slice(1);

type FlowItem = {
  program: string;
  raw: number;
  emitted: number;
  pct: number;
};

const FALLBACK: FlowItem[] = [
  { program: "rg", raw: 421000, emitted: 12600, pct: 97 },
];

function flowItems(stats: ImpactStats): FlowItem[] {
  const items = topPrograms(stats)
    .filter(
      (p) => Number(p.raw_bytes || 0) > 0 && Number(p.bytes_saved || 0) > 0
    )
    .map((p) => {
      const raw = Number(p.raw_bytes || 0);
      return {
        program: p.program,
        raw,
        emitted: Math.max(0, raw - Number(p.bytes_saved || 0)),
        pct: savedPct(p.bytes_saved, p.raw_bytes),
      };
    });
  return items.length ? items : FALLBACK;
}

export function Hero({ stats }: { stats: ImpactStats }) {
  const [copied, copy] = useCopy();
  const [agent, setAgent] = useState("claude");
  const reduce = useReducedMotion();
  const v = (variant: typeof fadeUp) => (reduce ? undefined : variant);

  const steps = [
    { key: "install", text: "curl -fsSL https://ctx-wire.dev/install.sh | sh" },
    { key: "init", text: `ctx-wire init ${agent}` },
    { key: "gain", text: "ctx-wire gain" },
  ];
  const copyAll = steps.map((s) => s.text).join("\n");

  return (
    <section
      id="top"
      className="grid w-full max-w-stage grid-cols-1 items-center gap-herogap pt-2 lg:grid-cols-2"
    >
      <motion.div
        variants={v(staggerContainer)}
        initial={reduce ? undefined : "hidden"}
        animate="visible"
      >
        <motion.p
          variants={v(fadeUp)}
          className="m-0 mb-4 inline-flex items-center gap-2.5 font-mono text-xs font-medium uppercase tracking-eyebrow text-green"
        >
          <span className="size-1.5 rounded-full bg-green shadow-dot" />
          context compression for AI coding agents
        </motion.p>

        <motion.h1
          variants={v(fadeUp)}
          className="m-0 mb-5 font-display text-hero font-extrabold text-head"
        >
          Cut the noise
          <br />
          on the{" "}
          <span className="bg-linear-to-r from-green to-teal bg-clip-text text-transparent">
            wire
          </span>
          .
        </motion.h1>

        <motion.p
          variants={v(fadeUp)}
          className="m-0 mb-7 max-w-copy font-mono text-sub leading-relaxed text-label"
        >
          ctx-wire runs your commands, compresses the output with declarative
          filters, scrubs secrets, and hands your agent a short result. The full
          log stays on disk for when something actually fails.
        </motion.p>

        <motion.div variants={v(fadeUp)} className="mb-4">
          <span className="mb-2.5 block font-mono text-2xs uppercase tracking-caps text-label">
            wire up your agent
          </span>
          <div
            role="tablist"
            aria-label="Agent"
            className="no-scrollbar inline-flex max-w-full gap-1 overflow-x-auto rounded-full bg-white/3 p-1 ring-1 ring-inset ring-line-soft"
          >
            {AGENTS.map((id) => {
              const active = id === agent;
              return (
                <button
                  type="button"
                  role="tab"
                  aria-selected={active}
                  key={id}
                  onClick={() => setAgent(id)}
                  className={`relative shrink-0 whitespace-nowrap rounded-full px-3 py-1 font-mono text-2xs transition-colors ${
                    active ? "text-green" : "text-label hover:text-fg"
                  }`}
                >
                  {active ? (
                    <motion.span
                      layoutId="agent-pill"
                      transition={
                        reduce
                          ? { duration: 0 }
                          : { type: "spring", stiffness: 420, damping: 32 }
                      }
                      className="absolute inset-0 rounded-full bg-green/15 ring-1 ring-inset ring-green/40"
                    />
                  ) : null}
                  <span className="relative z-10">{label(id)}</span>
                </button>
              );
            })}
          </div>
        </motion.div>

        <motion.div
          variants={v(fadeUp)}
          className="install-shadow max-w-md overflow-hidden rounded-card bg-linear-to-b from-panel to-screen"
        >
          <div className="flex items-center justify-between border-b border-line-soft bg-white/2 px-3.5 py-2.5">
            <div className="flex items-center gap-1.5">
              <span className="size-2 rounded-full bg-mac-close" />
              <span className="size-2 rounded-full bg-mac-min" />
              <span className="size-2 rounded-full bg-mac-zoom" />
              <span className="ml-1.5 font-mono text-2xs lowercase text-label">
                setup
              </span>
            </div>
            <motion.button
              type="button"
              whileTap={reduce ? undefined : { scale: 0.94 }}
              onClick={() => copy(copyAll)}
              className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 font-mono text-2xs transition-colors ${
                copied
                  ? "bg-green text-ink"
                  : "bg-green/10 text-green ring-1 ring-inset ring-green/25 hover:bg-green/20"
              }`}
            >
              {copied ? <IconCheck size={13} /> : <IconCopy size={13} />}
              {copied ? "copied" : "copy"}
            </motion.button>
          </div>
          <div className="flex flex-col p-1.5">
            {steps.map((step) => (
              <button
                type="button"
                key={step.key}
                onClick={() => copy(step.text)}
                title="Click to copy"
                className="flex w-full items-center gap-3 rounded-lg px-2.5 py-2 text-left font-mono text-sm text-fg transition-colors hover:bg-green/5"
              >
                <span className="select-none text-green">$</span>
                <code>
                  {step.key === "init" ? (
                    <>
                      ctx-wire init <span className="text-green">{agent}</span>
                    </>
                  ) : (
                    step.text
                  )}
                </code>
              </button>
            ))}
          </div>
        </motion.div>

        <motion.ul
          variants={v(fadeUp)}
          className="m-0 mt-5 flex list-none flex-wrap gap-2 p-0"
        >
          {TRUST.map((item) => (
            <li
              key={item}
              className="inline-flex items-center gap-1.5 rounded-full bg-white/3 px-2.5 py-1 font-mono text-2xs text-label ring-1 ring-inset ring-line-soft"
            >
              <IconCheck size={12} className="text-green" />
              {item}
            </li>
          ))}
        </motion.ul>
      </motion.div>

      <FlowDiagram items={flowItems(stats)} reduce={Boolean(reduce)} />
    </section>
  );
}

type Phase = "hold" | "out" | "in";

const HOLD_MS = 2400;
const OUT_MS = 520;
const IN_MS = 760;
const STEP_OUT = 2.5;
const STEP_IN = 4;

function FlowDiagram({
  items,
  reduce,
}: {
  items: FlowItem[];
  reduce: boolean;
}) {
  const [idx, setIdx] = useState(0);
  const [phase, setPhase] = useState<Phase>("hold");
  const [typed, setTyped] = useState(items[0]?.program ?? "");

  const item = items[idx % items.length];
  const pct = Math.min(99, Math.round(item.pct));
  const ratio = item.raw > 0 ? item.emitted / item.raw : 0;
  const kept = Math.max(
    1,
    Math.min(GRID_TOTAL, Math.round(ratio * GRID_TOTAL))
  );
  const v = (variant: typeof fadeUp) => (reduce ? undefined : variant);

  useEffect(() => {
    if (items.length <= 1 || reduce) return;
    let cancelled = false;
    const timers: number[] = [];
    const wait = (fn: () => void, ms: number) => {
      timers.push(
        window.setTimeout(() => {
          if (!cancelled) fn();
        }, ms)
      );
    };
    const loop = () => {
      setPhase("out");
      wait(() => {
        setIdx((p) => (p + 1) % items.length);
        setPhase("in");
        wait(() => {
          setPhase("hold");
          wait(loop, HOLD_MS);
        }, IN_MS);
      }, OUT_MS);
    };
    wait(loop, HOLD_MS);
    return () => {
      cancelled = true;
      for (const id of timers) window.clearTimeout(id);
    };
  }, [items.length, reduce]);

  useEffect(() => {
    const target = item.program;
    if (reduce || phase === "hold") {
      setTyped(target);
      return;
    }
    if (phase === "in") {
      setTyped("");
      let n = 0;
      const id = window.setInterval(
        () => {
          n += 1;
          setTyped(target.slice(0, n));
          if (n >= target.length) window.clearInterval(id);
        },
        Math.max(30, IN_MS / (target.length + 2))
      );
      return () => window.clearInterval(id);
    }
    let n = target.length;
    const id = window.setInterval(
      () => {
        n -= 1;
        setTyped(target.slice(0, Math.max(0, n)));
        if (n <= 0) window.clearInterval(id);
      },
      Math.max(22, OUT_MS / (target.length + 2))
    );
    return () => window.clearInterval(id);
  }, [phase, item.program, reduce]);

  return (
    <motion.div
      aria-hidden="true"
      variants={v(staggerContainer)}
      initial={reduce ? undefined : "hidden"}
      animate="visible"
      className="flex flex-col"
    >
      <motion.figure
        variants={v(scaleIn)}
        className="flow-card-bg m-0 rounded-panel border-t border-cyan/25 p-4"
      >
        <figcaption className="m-0 mb-3 flex items-center justify-between gap-3 font-mono text-xs">
          <span className="inline-flex items-center text-fg">
            <span className="mr-2 text-green">$</span>
            {typed}
            <span className="ctx-cursor ml-0.5 inline-block h-3.5 w-1.5 bg-cyan/70" />
          </span>
          <span className="text-2xs text-cyan">
            {formatBytes(item.raw)} raw
          </span>
        </figcaption>
        <CellGrid filled={GRID_TOTAL} phase={phase} tone="cyan" />
      </motion.figure>

      <div className="relative grid h-16 place-items-center">
        <span className="flow-beam absolute inset-y-0 left-1/2 w-0.5 -translate-x-1/2 motion-safe:animate-beam" />
        {phase === "in" && !reduce ? (
          <motion.span
            key={idx}
            initial={{ opacity: 0.5, scale: 0.5 }}
            animate={{ opacity: 0, scale: 1.9 }}
            transition={{ duration: 0.7, ease: "easeOut" }}
            className="pointer-events-none absolute z-0 size-24 rounded-full bg-green/30 blur-lg"
          />
        ) : null}
        <motion.span
          variants={v(scaleIn)}
          className="flow-node-bg relative z-10 inline-flex items-center gap-2 rounded-full px-3.5 py-1.5 font-mono text-sm font-bold text-ink"
        >
          <IconTerminal2 size={15} stroke={2.6} />
          ctx-wire
        </motion.span>
      </div>

      <motion.figure
        variants={v(scaleIn)}
        className="flow-card-bg relative m-0 rounded-panel border-t border-green/30 p-4"
      >
        <span className="absolute -top-2.5 right-4 rounded-full bg-green px-2.5 py-0.5 font-mono text-2xs font-bold text-ink shadow-badge">
          −{pct}% tokens
        </span>
        <figcaption className="m-0 mb-3 flex items-center justify-between gap-3 font-mono text-xs">
          <span className="text-green">agent context</span>
          <span className="text-2xs text-green">
            {formatBytes(item.emitted)} sent
          </span>
        </figcaption>
        <CellGrid filled={kept} phase={phase} tone="green" />
        <div className="mt-3 flex items-center justify-between font-mono text-2xs text-label">
          <span className="inline-flex items-center gap-1.5">
            <IconDatabase size={12} stroke={2} className="text-green" />
            sent to agent
          </span>
          <span className="inline-flex items-center gap-1.5">
            <IconScan size={12} stroke={2} className="text-white/20" />
            reclaimed context
          </span>
        </div>
      </motion.figure>

      {items.length > 1 ? (
        <div className="mt-4 flex items-center justify-center gap-1.5">
          {items.map((it, i) => (
            <span
              key={it.program}
              className={`h-1 rounded-full transition-all ${
                i === idx ? "w-4 bg-green" : "w-1 bg-dim"
              }`}
            />
          ))}
        </div>
      ) : null}
    </motion.div>
  );
}

function CellGrid({
  filled,
  phase,
  tone,
}: {
  filled: number;
  phase: Phase;
  tone: "cyan" | "green";
}) {
  const used = tone === "cyan" ? "text-cyan/80" : "text-green";
  const phaseClass = phase === "out" ? "is-out" : phase === "in" ? "is-in" : "";
  return (
    <div className={`ctx-grid grid grid-cols-cells gap-0.5 ${phaseClass}`}>
      {Array.from({ length: GRID_TOTAL }, (_, i) => i).map((i) => (
        <span
          key={i}
          className={`ctx-cell grid aspect-square place-items-center ${
            i < filled ? "is-used" : ""
          }`}
          style={
            {
              "--d-in": `${i * STEP_IN}ms`,
              "--d-out": `${(GRID_TOTAL - 1 - i) * STEP_OUT}ms`,
            } as CSSProperties
          }
        >
          <IconScan
            size={11}
            stroke={2}
            className="col-start-1 row-start-1 text-white/15"
          />
          <IconDatabase
            size={11}
            stroke={2}
            className={`filled-icon col-start-1 row-start-1 ${used}`}
          />
        </span>
      ))}
    </div>
  );
}
