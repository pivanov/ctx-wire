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
import type { TImpactStats } from "../types";
import { SectionEyebrow } from "./section-heading";

const AGENTS = [
  "claude",
  "codex",
  "cursor",
  "gemini",
  "copilot",
  "cline",
  "windsurf",
  "antigravity",
  "hermes",
  "kilocode",
  "opencode",
  "pi",
  "visualstudio",
  "vscode",
];

const TRUST = [
  "147 filters, 400+ tests",
  "fail-closed scrubbing",
  "failures pass through intact",
  "inspect what's filtered",
  "runs fully local",
  "MIT licensed",
];

const GRID_TOTAL = 140;

const INSTALL = "curl -fsSL https://ctx-wire.dev/install.sh | sh";

const AGENT_LABELS: Record<string, string> = {
  opencode: "OpenCode",
  kilocode: "Kilo Code",
  vscode: "VS Code",
  visualstudio: "Visual Studio",
};

const label = (id: string) =>
  AGENT_LABELS[id] ?? id.charAt(0).toUpperCase() + id.slice(1);

type TFlowItem = {
  program: string;
  raw: number;
  emitted: number;
  pct: number;
};

const FALLBACK: TFlowItem[] = [
  { program: "rg", raw: 421000, emitted: 12600, pct: 97 },
];

const flowItems = (stats: TImpactStats): TFlowItem[] => {
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
};

export const Hero = ({ stats }: { stats: TImpactStats }) => {
  const [agent, setAgent] = useState("claude");
  const reduce = useReducedMotion();
  const v = (variant: typeof fadeUp) => (reduce ? undefined : variant);

  return (
    <section id="top" className="w-full max-w-stage pt-2">
      <div className="grid grid-cols-1 items-center lg:grid-cols-2">
        <motion.div
          variants={v(staggerContainer)}
          initial={reduce ? undefined : "hidden"}
          whileInView="visible"
          viewport={{ once: true, amount: 0.1 }}
        >
          <SectionEyebrow className="mb-4">
            context compression for AI coding agents
          </SectionEyebrow>

          <motion.h1
            variants={v(fadeUp)}
            className="m-0 mb-5 font-display text-hero font-extrabold text-head"
          >
            Cut the noise.
            <br />
            Keep the{" "}
            <span className="bg-linear-to-r from-green to-teal bg-clip-text text-transparent">
              signal
            </span>
            .
          </motion.h1>

          <motion.p
            variants={v(fadeUp)}
            className="m-0 max-w-copy font-mono text-sub leading-relaxed text-label"
          >
            ctx-wire runs your commands, compresses the output with declarative
            filters, scrubs secrets, and hands your agent a short result. The
            full log stays on disk for when something actually fails.
          </motion.p>
        </motion.div>

        <FlowDiagram items={flowItems(stats)} reduce={Boolean(reduce)} />
      </div>

      <motion.div
        variants={v(staggerContainer)}
        initial={reduce ? undefined : "hidden"}
        whileInView="visible"
        viewport={{ once: true, amount: 0.1 }}
        className="mt-12"
      >
        <motion.div variants={v(fadeUp)}>
          <span className="mb-2.5 block font-mono text-2xs uppercase tracking-caps text-label">
            wire up your agent{" "}
            <span className="text-dim">· {AGENTS.length} supported</span>
          </span>
          <div
            role="tablist"
            aria-label="Agent"
            className="flex w-full flex-wrap gap-1.5 rounded-2xl bg-white/3 p-1.5 ring-1 ring-inset ring-line-soft"
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
                  className={`relative shrink-0 whitespace-nowrap rounded-full px-3.5 py-1.5 font-mono text-2xs transition-[color,transform] duration-150 ease-out motion-safe:active:scale-[0.97] ${
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
          className="mt-5 grid grid-cols-1 gap-4 sm:grid-cols-steps"
        >
          <div className="flex flex-col">
            <StepLabel n={1} title="Download" />
            <CommandBox command={INSTALL} reduce={Boolean(reduce)} />
          </div>
          <div className="flex flex-col">
            <StepLabel n={2} title="Init" />
            <CommandBox
              command={`ctx-wire init ${agent}`}
              agent={agent}
              reduce={Boolean(reduce)}
            />
          </div>
          <div className="flex flex-col">
            <StepLabel n={3} title="Enjoy the gain" />
            <CommandBox command="ctx-wire gain" reduce={Boolean(reduce)} />
          </div>
        </motion.div>

        <motion.ul
          variants={v(fadeUp)}
          className="m-0 mt-5 flex list-none flex-wrap items-center gap-2 p-0"
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
    </section>
  );
};

const StepLabel = ({ n, title }: { n: number; title: string }) => {
  return (
    <div className="mb-2.5 flex items-center gap-2.5">
      <span className="grid size-6 place-items-center rounded-md bg-green font-mono text-2xs font-bold text-ink">
        {n}
      </span>
      <span className="font-mono text-cap font-bold text-head">{title}</span>
    </div>
  );
};

const CommandBox = ({
  agent,
  command,
  reduce,
}: {
  agent?: string;
  command: string;
  reduce: boolean;
}) => {
  const [copied, copy] = useCopy();
  return (
    <motion.button
      type="button"
      whileTap={reduce ? undefined : { scale: 0.985 }}
      onClick={() => copy(command)}
      title="Click to copy"
      className="install-shadow group relative flex w-full grow items-center gap-2.5 rounded-card border border-line-soft bg-linear-to-b from-panel to-screen px-4 py-3.5 pr-9 text-left font-mono text-cap transition-colors hover:border-green/30"
    >
      <span className="shrink-0 select-none text-green">$</span>
      <code className="min-w-0 wrap-break-word leading-relaxed text-fg">
        {agent ? (
          <>
            ctx-wire init <span className="text-green">{agent}</span>
          </>
        ) : (
          command
        )}
      </code>
      <span className="absolute right-3 top-1/2 -translate-y-1/2">
        {copied ? (
          <IconCheck size={14} className="text-green" />
        ) : (
          <IconCopy
            size={14}
            className="text-label transition-colors group-hover:text-green"
          />
        )}
      </span>
    </motion.button>
  );
};

type TPhase = "hold" | "out" | "in";

const HOLD_MS = 2400;
const OUT_MS = 520;
const IN_MS = 760;
const STEP_OUT = 2.5;
const STEP_IN = 4;

const FlowDiagram = ({
  items,
  reduce,
}: {
  items: TFlowItem[];
  reduce: boolean;
}) => {
  const [idx, setIdx] = useState(0);
  const [phase, setPhase] = useState<TPhase>("hold");
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
    if (items.length <= 1 || reduce) {
      return;
    }
    let cancelled = false;
    const timers: number[] = [];
    const wait = (fn: () => void, ms: number) => {
      timers.push(
        window.setTimeout(() => {
          if (!cancelled) {
            fn();
          }
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
      for (const id of timers) {
        window.clearTimeout(id);
      }
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
          if (n >= target.length) {
            window.clearInterval(id);
          }
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
        if (n <= 0) {
          window.clearInterval(id);
        }
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
              className={`h-1 rounded-full transition-[width,background-color] ease-out ${
                i === idx ? "w-4 bg-green" : "w-1 bg-dim"
              }`}
            />
          ))}
        </div>
      ) : null}
    </motion.div>
  );
};

const CellGrid = ({
  filled,
  phase,
  tone,
}: {
  filled: number;
  phase: TPhase;
  tone: "cyan" | "green";
}) => {
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
};
