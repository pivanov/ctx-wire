import { motion, useReducedMotion } from "motion/react";
import { fadeUp, staggerContainer } from "../lib/variants";

const STEPS = [
  {
    n: 1,
    title: "Run",
    desc: "Your agent's command runs through ctx-wire: a Bash hook, a PATH shim, or an MCP tool.",
  },
  {
    n: 2,
    title: "Compress & scrub",
    desc: "142 declarative filters shrink the output; secrets are scrubbed fail-closed; the full log stays on disk, and inspect shows exactly what was cut.",
  },
  {
    n: 3,
    title: "Short result",
    desc: "The agent gets a short result and pays a fraction of the tokens.",
  },
];

const CAPS = [
  {
    name: "MCP server",
    desc: "Filtering as a tool (run_command + read_file) for MCP-native editors.",
  },
  {
    name: "PATH shims",
    desc: "Catches commands nested scripts spawn, not just the Bash hook.",
  },
  {
    name: "Fail-closed scrubbing",
    desc: "Redacts all argv + output; withholds rather than leak a secret.",
  },
  {
    name: "Streaming-aware bypass",
    desc: "Auto-detects dev servers, watchers, interactive. No deadlocks.",
  },
  {
    name: "Per-agent attribution",
    desc: "Savings split by agent (Claude, Codex, Cursor, Gemini, Copilot).",
  },
  {
    name: "142 filters · 326 tests",
    desc: "Declarative TOML corpus, conformance-tested every release.",
  },
  {
    name: "See what was cut",
    desc: "inspect shows raw-vs-filtered for any recent command, so you can audit what was hidden (opt-in retention).",
  },
  {
    name: "Author + share filters",
    desc: "tune draft scaffolds a filter from real output; pull community filters or publish your own.",
  },
  {
    name: "Wrap an MCP server",
    desc: "mcp-wrap relays any MCP server and measures what each tool costs you in tokens.",
  },
];

const eyebrow =
  "m-0 inline-flex items-center gap-2.5 font-mono text-xs font-medium uppercase tracking-eyebrow text-green";

export function HowItWorks() {
  const reduce = useReducedMotion();
  const v = (variant: typeof fadeUp) => (reduce ? undefined : variant);

  return (
    <motion.section
      aria-label="How ctx-wire works"
      variants={v(staggerContainer)}
      initial={reduce ? undefined : "hidden"}
      whileInView="visible"
      viewport={{ once: true, amount: 0.2 }}
      className="globe-card-bg w-full max-w-stage rounded-section p-cardpad"
    >
      <motion.p variants={v(fadeUp)} className={eyebrow}>
        <span className="size-1.5 rounded-full bg-green shadow-dot" />
        how it works
      </motion.p>

      <motion.div
        variants={v(fadeUp)}
        className="mt-5 grid grid-cols-1 gap-x-8 gap-y-6 sm:grid-cols-3"
      >
        {STEPS.map((step) => (
          <div key={step.n}>
            <div className="flex items-center gap-3">
              <span className="grid size-7 place-items-center rounded-lg bg-green font-mono text-sm font-bold text-ink">
                {step.n}
              </span>
              <h3 className="m-0 font-mono text-base font-bold text-head">
                {step.title}
              </h3>
            </div>
            <p className="m-0 mt-2.5 font-mono text-cap leading-relaxed text-label">
              {step.desc}
            </p>
          </div>
        ))}
      </motion.div>

      <div className="my-7 border-t border-line-soft" />

      <motion.p variants={v(fadeUp)} className={eyebrow}>
        <span className="size-1.5 rounded-full bg-green shadow-dot" />
        what makes it different
      </motion.p>

      <motion.div
        variants={v(staggerContainer)}
        className="mt-4 grid grid-cols-1 gap-6 sm:grid-cols-2 lg:grid-cols-3"
      >
        {CAPS.map((cap) => (
          <motion.div
            key={cap.name}
            variants={v(fadeUp)}
            className="rounded-card bg-green/4 p-3.5 ring-1 ring-inset ring-line"
          >
            <div className="font-mono text-cap font-bold text-green">
              {cap.name}
            </div>
            <div className="mt-1 font-mono text-2xs leading-relaxed text-label">
              {cap.desc}
            </div>
          </motion.div>
        ))}
      </motion.div>
    </motion.section>
  );
}
