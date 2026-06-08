import { RiArrowRightUpLine } from "@remixicon/react";
import { motion, useReducedMotion } from "motion/react";
import { fadeUp, staggerContainer } from "../lib/variants";

const RTK_URL = "https://github.com/rtk-ai/rtk";

type Row = { dim: string; rtk: string; ctx: string; note: string };

// Functional comparison only (not packaging or maturity). Each row credits
// rtk's solid baseline, then shows where ctx-wire takes the same job further.
// rtk is the respected groundwork, never the punchline.
const ROWS: Row[] = [
  {
    dim: "Catching the command",
    rtk: "Agent hooks rewrite the command before it runs.",
    ctx: "Hooks too, plus PATH shims that catch commands nested scripts spawn, and an MCP tool for editors that speak it.",
    note: "wider net",
  },
  {
    dim: "Scrubbing secrets",
    rtk: "Keeps secrets out of its telemetry and masks sensitive env vars.",
    ctx: "Also scrubs secrets from the output the agent sees: global, and fail-closed (it withholds rather than leak).",
    note: "fail-closed",
  },
  {
    dim: "Dev servers & watchers",
    rtk: "Skip the long-lived ones with an exclude list you maintain.",
    ctx: "Auto-detects dev servers, watchers, and interactive commands and steps aside. No deadlocks, no config.",
    note: "automatic",
  },
  {
    dim: "Crediting the savings",
    rtk: "Totals the tokens saved per command and per project.",
    ctx: "Also splits every saving by the agent that caused it (Claude, Codex, Cursor, Gemini, Copilot).",
    note: "per agent",
  },
  {
    dim: "Diagnosing a command",
    rtk: "Shows whether a command would be rewritten.",
    ctx: "explain breaks down the filter, the mode, and why; doctor checks the whole setup end to end.",
    note: "deeper",
  },
];

const eyebrow =
  "m-0 inline-flex items-center gap-2.5 font-mono text-xs font-medium uppercase tracking-eyebrow text-green";

export function ComparisonRtk() {
  const reduce = useReducedMotion();
  const v = (variant: typeof fadeUp) => (reduce ? undefined : variant);

  return (
    <motion.section
      aria-label="ctx-wire and rtk"
      variants={v(staggerContainer)}
      initial={reduce ? undefined : "hidden"}
      whileInView="visible"
      viewport={{ once: true, amount: 0.15 }}
      className="globe-card-bg w-full max-w-stage rounded-section p-cardpad"
    >
      <motion.p variants={v(fadeUp)} className={eyebrow}>
        <span className="size-1.5 rounded-full bg-green shadow-dot" />
        inspired by rtk
      </motion.p>

      <motion.p
        variants={v(fadeUp)}
        className="m-0 mt-4 font-mono text-sub leading-relaxed text-label"
      >
        ctx-wire wouldn't exist without{" "}
        <a
          href={RTK_URL}
          target="_blank"
          rel="noreferrer"
          className="text-teal underline-offset-2 hover:underline"
        >
          rtk
        </a>{" "}
        (Rust Token Killer). It proved you can sit between an agent and the
        shell, filter the noise, and hand back a fraction of the tokens.
        ctx-wire builds on that groundwork and takes the same jobs a step
        further.
      </motion.p>

      <motion.div
        variants={v(staggerContainer)}
        initial={reduce ? undefined : "hidden"}
        whileInView="visible"
        viewport={{ once: true, amount: 0.1 }}
        className="mt-8"
      >
        {ROWS.map((row) => (
          <motion.div
            key={row.dim}
            variants={v(fadeUp)}
            className="border-t border-line-soft py-5 first:border-t-0 first:pt-0"
          >
            <div className="mb-3 font-mono text-2xs uppercase tracking-caps text-label">
              {row.dim}
            </div>
            <div className="grid grid-cols-1 gap-x-10 gap-y-3 sm:grid-cols-2">
              <Side tool="rtk" accent="teal" text={row.rtk} />
              <Side
                tool="ctx-wire"
                accent="green"
                text={row.ctx}
                note={row.note}
              />
            </div>
          </motion.div>
        ))}
      </motion.div>

      <motion.a
        variants={v(fadeUp)}
        href={RTK_URL}
        target="_blank"
        rel="noreferrer"
        className="mt-6 inline-flex items-center gap-1.5 font-mono text-cap text-teal transition-opacity hover:opacity-80"
      >
        Check out rtk
        <RiArrowRightUpLine size={15} />
      </motion.a>
    </motion.section>
  );
}

function Side({
  tool,
  accent,
  text,
  note,
}: {
  tool: string;
  accent: "teal" | "green";
  text: string;
  note?: string;
}) {
  const isTeal = accent === "teal";
  return (
    <div>
      <div className="mb-1.5 flex items-center gap-2">
        <span
          className={`size-1.5 rounded-full ${isTeal ? "bg-teal" : "bg-green"}`}
        />
        <span
          className={`font-mono text-2xs font-bold lowercase ${isTeal ? "text-teal" : "text-green"}`}
        >
          {tool}
        </span>
        {note ? (
          <span className="ml-auto rounded-full bg-green/12 px-2 py-0.5 font-mono text-2xs text-green">
            {note}
          </span>
        ) : null}
      </div>
      <p
        className={`m-0 font-mono text-2xs leading-relaxed ${isTeal ? "text-label" : "text-fg"}`}
      >
        {text}
      </p>
    </div>
  );
}
