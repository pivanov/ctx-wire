import { motion, useReducedMotion } from "motion/react";
import {
  formatBytes,
  formatInt,
  formatTokens,
  savedPct,
  topPrograms,
} from "../format";
import { useTween } from "../hooks/use-tween";
import { fadeUp } from "../lib/variants";
import type { TImpactStats, TProgramStats } from "../types";

type TProps = {
  stats: TImpactStats;
};

export const TerminalWindow = ({ stats }: TProps) => {
  const totals = stats.totals || {};
  const programs = topPrograms(stats);
  const moreCount = (stats.programs?.length || 0) - programs.length;
  const reduce = useReducedMotion();

  const commands = useTween(Number(totals.commands || 0));
  const raw = useTween(Number(totals.raw_bytes || 0));
  const emitted = useTween(Number(totals.emitted_bytes || 0));
  const saved = useTween(Number(totals.bytes_saved || 0));
  const tokens = useTween(
    Number(
      totals.tokens_saved || Math.ceil(Number(totals.bytes_saved || 0) / 4)
    )
  );

  const pct = savedPct(saved, raw);
  const maxSaved = Math.max(
    ...programs.map((p) => Number(p.bytes_saved || 0)),
    1
  );
  const live = Number(totals.commands || 0) > 0;

  return (
    <motion.section
      aria-label="ctx-wire gain terminal"
      variants={reduce ? undefined : fadeUp}
      initial={reduce ? undefined : "hidden"}
      whileInView="visible"
      viewport={{ once: true, amount: 0.1 }}
      className="window-shadow w-full overflow-hidden rounded-window bg-screen"
    >
      <header className="titlebar-bg relative flex h-8 items-center px-3.5">
        <div className="flex items-center gap-2">
          <span className="size-2.5 rounded-full bg-mac-close ring-1 ring-inset ring-black/15" />
          <span className="size-2.5 rounded-full bg-mac-min ring-1 ring-inset ring-black/15" />
          <span className="size-2.5 rounded-full bg-mac-zoom ring-1 ring-inset ring-black/15" />
        </div>

        <div className="pointer-events-none absolute inset-0 flex items-center justify-center gap-2 font-mono text-2xs text-chrome">
          <span className="text-green">›_</span>
          ctx-wire gain
        </div>
      </header>

      <div className="screen">
        <div className="scan" aria-hidden="true" />
        <div className="glare" aria-hidden="true" />
        {live ? null : <ConnectingState />}
        <div
          className={`relative z-10 m-0 w-full font-mono text-term text-fg ${live ? "" : "hidden"}`}
          aria-live="polite"
        >
          <div className="mb-1 whitespace-pre">
            <span className="text-green">🚀</span> ctx-wire gain
          </div>
          <div className="mb-3 whitespace-pre font-bold">
            <span className="text-green">ctx-wire gain</span>
            <span className="text-label">: summary</span>
          </div>
          <div className="mb-5 border-t border-line-soft" />

          <dl className="m-0 mb-6 grid gap-0.5">
            <Summary
              label="Total commands"
              value={formatInt(commands)}
              tone="text-cyan"
            />
            <Summary
              label="Raw bytes"
              value={formatBytes(raw)}
              tone="text-cyan"
            />
            <Summary
              label="Emitted bytes"
              value={formatBytes(emitted)}
              tone="text-cyan"
            />
            <Summary
              label="Bytes saved"
              value={formatBytes(saved)}
              suffix={`(${pct.toFixed(1)}%)`}
              tone="text-green"
            />
            <Summary
              label="Saved tokens"
              value={formatTokens(tokens)}
              tone="text-cyan"
            />
            <div className="flex items-baseline gap-4">
              <dt className="m-0 w-36 shrink-0 text-label sm:w-44">
                Efficiency:
              </dt>
              <dd className="m-0 flex min-w-0 items-baseline gap-2.5">
                <Meter value={pct} width={28} />
                {/* Brand exception: the headline percent stays green even in the
                    30-70 band where the CLI would render yellow. */}
                <span className="text-green">({pct.toFixed(1)}%)</span>
              </dd>
            </div>
          </dl>

          <div className="mb-2 whitespace-pre font-bold text-green">
            By Program
          </div>
          <table className="program-table">
            <thead>
              <tr>
                <th>#</th>
                <th>Program</th>
                <th>Count</th>
                <th>Saved</th>
                <th>Avg%</th>
                <th>Impact</th>
              </tr>
            </thead>
            <tbody>
              {programs.map((program, index) => (
                <ProgramRow
                  key={program.program}
                  index={index}
                  maxSaved={maxSaved}
                  program={program}
                />
              ))}
            </tbody>
          </table>
          {moreCount > 0 ? (
            <div className="mt-3 whitespace-pre text-dim">
              ... {formatInt(moreCount)} more programs
            </div>
          ) : null}
        </div>
      </div>
    </motion.section>
  );
};

// pctTone mirrors the CLI's value-based percent coloring (ui.Theme.PercentBare:
// >=70 green, >=30 yellow, else dim), so the replica colors match a real run.
const pctTone = (v: number): string => {
  if (v >= 70) {
    return "text-green";
  }
  if (v >= 30) {
    return "text-yellow";
  }
  return "text-dim";
};

const Summary = ({
  label,
  suffix,
  suffixTone,
  tone,
  value,
}: {
  label: string;
  suffix?: string;
  suffixTone?: string;
  tone: string;
  value: string;
}) => {
  return (
    <div className="flex items-baseline gap-4">
      <dt className="m-0 w-36 shrink-0 text-label sm:w-44">{label}:</dt>
      <dd className="m-0">
        <span className={tone}>{value}</span>
        {suffix ? <span className={suffixTone ?? tone}> {suffix}</span> : null}
      </dd>
    </div>
  );
};

const ProgramRow = ({
  index,
  maxSaved,
  program,
}: {
  index: number;
  maxSaved: number;
  program: TProgramStats;
}) => {
  const runs = useTween(Number(program.runs ?? program.count ?? 0));
  const saved = useTween(Number(program.bytes_saved || 0));
  const raw = useTween(Number(program.raw_bytes || 0));
  const avg = savedPct(saved, raw);
  const impact = (saved / maxSaved) * 100;

  return (
    <tr>
      <td>{index + 1}.</td>
      <td>{program.program}</td>
      <td>{formatInt(runs)}</td>
      <td>{formatBytes(saved)}</td>
      <td className={pctTone(avg)}>{avg.toFixed(1)}%</td>
      <td>
        <Meter value={impact} width={18} compact />
      </td>
    </tr>
  );
};

// Shown until the first telemetry payload arrives. Zeros are never rendered;
// a gain report full of "0 B (0.0%)" reads as a dead project, not a loading one.
const ConnectingState = () => {
  return (
    <div
      className="relative z-10 m-0 w-full font-mono text-term text-fg"
      aria-live="polite"
    >
      <div className="mb-1 whitespace-pre">
        <span className="text-green">🚀</span> ctx-wire gain
      </div>
      <div className="mb-3 whitespace-pre">
        <span className="font-bold text-green">ctx-wire gain</span>
        <span className="text-label">: connecting to telemetry</span>
        <span className="ctx-cursor ml-1 inline-block h-3 w-1.5 bg-green/70 align-middle" />
      </div>
      <div className="mb-5 border-t border-line-soft" />
      <div className="grid max-w-md gap-2.5" aria-hidden="true">
        {[
          ["w-36", "w-16"],
          ["w-28", "w-24"],
          ["w-32", "w-20"],
          ["w-40", "w-28"],
        ].map(([labelW, valueW]) => (
          <div key={`${labelW}-${valueW}`} className="flex items-center gap-6">
            <span className={`shimmer h-3 ${labelW}`} />
            <span className={`shimmer h-3 ${valueW}`} />
          </div>
        ))}
      </div>
    </div>
  );
};

const Meter = ({
  compact = false,
  value,
  width,
}: {
  compact?: boolean;
  value: number;
  width: number;
}) => {
  const filled = Math.max(
    0,
    Math.min(width, Math.round((value / 100) * width))
  );
  return (
    <span
      className={compact ? "meter meter-compact" : "meter"}
      aria-hidden="true"
    >
      {/* The CLI renders the fill as a light shade; on the web that washes out
          at this font size, so the replica keeps the denser glyph (the "hard"
          green bar is part of the site's look). */}
      <span className="text-green">{"▓".repeat(filled)}</span>
      <span className="text-dim">{"░".repeat(width - filled)}</span>
    </span>
  );
};
