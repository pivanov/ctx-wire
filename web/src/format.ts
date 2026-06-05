import type { CountryStats, ImpactStats, ProgramStats } from "./types";

export function formatBytes(value?: number): string {
  const n = Number(value || 0);
  if (n < 1024) return `${Math.round(n)} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  return `${(n / 1024 / 1024 / 1024).toFixed(1)} GB`;
}

export function formatTokens(value?: number): string {
  const n = Number(value || 0);
  if (n < 1000) return `~${Math.round(n)}`;
  if (n < 1_000_000) return `~${(n / 1000).toFixed(1)}K`;
  return `~${(n / 1_000_000).toFixed(1)}M`;
}

export function formatCompact(value?: number): string {
  const n = Number(value || 0);
  if (n < 1000) return formatInt(n);
  if (n < 1_000_000) return `${(n / 1000).toFixed(1)}K`;
  if (n < 1_000_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  return `${(n / 1_000_000_000).toFixed(1)}B`;
}

export function formatInt(value?: number): string {
  return Math.round(Number(value || 0)).toLocaleString("en-US");
}

export function formatUsd(value?: number): string {
  const n = Math.round(Number(value || 0));
  if (n < 1_000_000) return `$${n.toLocaleString("en-US")}`;
  return `$${(n / 1_000_000).toFixed(1)}M`;
}

export function savedPct(saved?: number, raw?: number): number {
  const rawValue = Number(raw || 0);
  if (rawValue <= 0) return 0;
  return (Number(saved || 0) / rawValue) * 100;
}

export function countryCode(country: CountryStats): string {
  return String(country.country || country.country_code || country.code || "")
    .trim()
    .toUpperCase();
}

export function flagEmoji(code: string): string {
  const cc = code.trim().toUpperCase();
  if (cc.length !== 2 || !/^[A-Z]{2}$/.test(cc)) return "🏳";
  const base = 0x1f1e6;
  return String.fromCodePoint(
    base + (cc.charCodeAt(0) - 65),
    base + (cc.charCodeAt(1) - 65)
  );
}

export function shareOf(value?: number, total?: number): number {
  const t = Number(total || 0);
  if (t <= 0) return 0;
  return Math.min(100, (Number(value || 0) / t) * 100);
}

export function topPrograms(stats: ImpactStats): ProgramStats[] {
  return [...(stats.programs || [])]
    .sort((a, b) => Number(b.bytes_saved || 0) - Number(a.bytes_saved || 0))
    .slice(0, 10);
}

export function topCountries(stats: ImpactStats): CountryStats[] {
  return [...(stats.countries || [])]
    .filter(
      (c) =>
        countryCode(c) &&
        Number(c.bytes_saved || c.tokens_saved || c.commands || 0) > 0
    )
    .sort((a, b) => Number(b.bytes_saved || 0) - Number(a.bytes_saved || 0))
    .slice(0, 10);
}

export function reportText(stats: ImpactStats): string {
  const totals = stats.totals || {};
  const programs = topPrograms(stats);
  const pct = savedPct(totals.bytes_saved, totals.raw_bytes);
  const meterWidth = 28;
  const filled = Math.max(
    0,
    Math.min(meterWidth, Math.round((pct / 100) * meterWidth))
  );
  const meter = `${"░".repeat(filled)}${"░".repeat(meterWidth - filled)}`;
  const maxSaved = Math.max(
    ...programs.map((p) => Number(p.bytes_saved || 0)),
    1
  );
  const rows = programs.map((p, i) => {
    const runs = p.runs ?? p.count ?? 0;
    const avg = savedPct(p.bytes_saved, p.raw_bytes);
    const impactFilled = Math.max(
      0,
      Math.min(18, Math.round((Number(p.bytes_saved || 0) / maxSaved) * 18))
    );
    const impact = `${"░".repeat(impactFilled)}${"░".repeat(18 - impactFilled)}`;
    return `${String(i + 1).padStart(3)}. │ ${p.program.padEnd(8)} │ ${String(runs).padStart(5)} │ ${formatBytes(p.bytes_saved).padStart(8)} │ ${`${avg.toFixed(1)}%`.padStart(5)} │ ${impact}`;
  });

  return [
    "ctx-wire gain",
    "",
    `Reported installs: ${formatInt(totals.installs).padStart(8)}`,
    `Total commands:    ${formatInt(totals.commands).padStart(8)}`,
    `Raw bytes:         ${formatBytes(totals.raw_bytes).padStart(8)}`,
    `Emitted bytes:     ${formatBytes(totals.emitted_bytes).padStart(8)}`,
    `Bytes saved:       ${formatBytes(totals.bytes_saved).padStart(8)} (${pct.toFixed(1)}%)`,
    `Saved tokens:      ${formatTokens(totals.tokens_saved || Math.ceil(Number(totals.bytes_saved || 0) / 4))}`,
    `Efficiency meter:  [${meter}] (${pct.toFixed(1)}%)`,
    "",
    "By Program",
    "────┬──────────┬───────┬──────────┬───────┬────────────────────",
    "  # │ Program  │ Count │    Saved │  Avg% │ Impact",
    "────┼──────────┼───────┼──────────┼───────┼────────────────────",
    ...rows,
    "────┴──────────┴───────┴──────────┴───────┴────────────────────",
  ].join("\n");
}
