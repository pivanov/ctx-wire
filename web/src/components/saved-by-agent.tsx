import { motion, useReducedMotion } from "motion/react";
import { formatInt, formatTokens } from "../format";
import { fadeUp, staggerContainer } from "../lib/variants";
import type { ImpactStats } from "../types";
import { AgentLogo, agentLabel } from "./agent-logos";

export function SavedByAgent({ stats }: { stats: ImpactStats }) {
  const reduce = useReducedMotion();
  const agents = (stats.agents ?? [])
    .filter((a) => Number(a.tokens_saved || 0) > 0)
    .slice(0, 6);

  if (agents.length === 0) {
    return null;
  }

  const max = Math.max(...agents.map((a) => Number(a.tokens_saved || 0)), 1);

  return (
    <motion.section
      aria-label="Token savings by agent"
      variants={reduce ? undefined : staggerContainer}
      initial={reduce ? undefined : "hidden"}
      whileInView="visible"
      viewport={{ once: true, amount: 0.2 }}
      className="globe-card-bg w-full max-w-stage rounded-section p-cardpad"
    >
      <motion.p
        variants={reduce ? undefined : fadeUp}
        className="m-0 inline-flex items-center gap-2.5 font-mono text-xs font-medium uppercase tracking-eyebrow text-green"
      >
        <span className="size-1.5 rounded-full bg-green shadow-dot" />
        saved by agent
      </motion.p>
      <motion.p
        variants={reduce ? undefined : fadeUp}
        className="m-0 mt-3 mb-6 font-mono text-cap text-label"
      >
        Token savings attributed to each coding agent, live from telemetry.
      </motion.p>

      <motion.div
        variants={reduce ? undefined : staggerContainer}
        className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3"
      >
        {agents.map((agent, index) => {
          const tokens = Number(agent.tokens_saved || 0);
          const share = Math.max(4, Math.round((tokens / max) * 100));
          const lead = index === 0;
          return (
            <motion.div
              key={agent.agent}
              variants={reduce ? undefined : fadeUp}
              className="rounded-card bg-white/3 p-4 ring-1 ring-inset ring-line"
            >
              <div className="flex items-center justify-between gap-3">
                <div className="flex items-center gap-2.5">
                  <span className="grid size-9 place-items-center rounded-lg bg-white ring-1 ring-inset ring-black/10">
                    <AgentLogo name={agent.agent} size={18} />
                  </span>
                  <span className="font-mono text-sm font-bold text-head">
                    {agentLabel(agent.agent)}
                  </span>
                </div>
                <span className="font-mono text-2xs text-label">
                  {formatInt(agent.runs)} cmds
                </span>
              </div>
              <div className="mt-3.5 font-mono text-xl font-bold tabular-nums text-head">
                {formatTokens(tokens)}{" "}
                <span className="font-mono text-cap font-medium text-label">
                  tokens
                </span>
              </div>
              <div className="mt-2.5 h-1 overflow-hidden rounded-full bg-white/5">
                <div
                  className={`h-full rounded-full ${lead ? "bg-green" : "bg-cyan/60"}`}
                  style={{ width: `${share}%` }}
                />
              </div>
            </motion.div>
          );
        })}
      </motion.div>
    </motion.section>
  );
}
