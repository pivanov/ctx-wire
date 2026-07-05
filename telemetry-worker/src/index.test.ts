import { describe, expect, it } from "vitest";
import { sanitizeImpact } from "./index";

const base = { schema: 1, event: "impact" };

describe("sanitizeImpact", () => {
  it("passes a stock client report through unchanged", () => {
    const r = sanitizeImpact({
      ...base,
      commands: 220,
      raw_bytes: 1_000_000,
      emitted_bytes: 100_000,
      bytes_saved: 900_000,
      tokens_saved: 225_000, // ceil(900k / 4), the client's estimator
      programs: {
        git: { runs: 120, raw_bytes: 600_000, emitted_bytes: 60_000, bytes_saved: 540_000, tokens_saved: 135_000 },
        rg: { runs: 100, raw_bytes: 400_000, emitted_bytes: 40_000, bytes_saved: 360_000, tokens_saved: 90_000 },
      },
      agents: {
        claude: { runs: 220, raw_bytes: 1_000_000, emitted_bytes: 100_000, bytes_saved: 900_000, tokens_saved: 225_000 },
      },
    });
    expect(r.flags).toEqual([]);
    expect(r.commands).toBe(220);
    expect(r.rawBytes).toBe(1_000_000);
    expect(r.emittedBytes).toBe(100_000);
    expect(r.bytesSaved).toBe(900_000);
    expect(r.tokensSaved).toBe(225_000);
    expect(r.programs).toHaveLength(2);
    expect(r.agents).toHaveLength(1);
  });

  it("does NOT clip on_empty savings where emitted exceeds raw", () => {
    // Regression: the client floors each command's saved to 0 when a synthetic
    // on_empty message makes emitted exceed raw, so aggregate saved legitimately
    // exceeds raw - emitted. A raw-minus-emitted bound wrongly flagged and, when
    // emitted approached raw, zeroed genuine savings. saved <= raw must pass it.
    const r = sanitizeImpact({
      ...base,
      commands: 1000,
      raw_bytes: 1_000_000,
      emitted_bytes: 950_000, // near raw: mostly empty searches this window
      bytes_saved: 900_000, // floored per-command sum, exceeds raw - emitted
      tokens_saved: 225_000,
    });
    expect(r.bytesSaved).toBe(900_000);
    expect(r.tokensSaved).toBe(225_000);
    expect(r.flags).toEqual([]);
  });

  it("still bounds saved by raw", () => {
    const r = sanitizeImpact({
      ...base,
      commands: 10,
      raw_bytes: 1_000,
      emitted_bytes: 100,
      bytes_saved: 5_000, // impossible: saved cannot exceed what was produced
      tokens_saved: 250,
    });
    expect(r.bytesSaved).toBe(1_000);
  });

  it("clamps token claims no byte volume could encode", () => {
    const r = sanitizeImpact({
      ...base,
      commands: 10,
      raw_bytes: 2_000,
      emitted_bytes: 0,
      bytes_saved: 1_000,
      tokens_saved: 299_000_000,
    });
    expect(r.tokensSaved).toBe(500);
    expect(r.flags).toContain("tokens_saved_over_ratio");
  });

  it("still caps emitted at raw", () => {
    const r = sanitizeImpact({ ...base, commands: 1, raw_bytes: 100, emitted_bytes: 500, bytes_saved: 0, tokens_saved: 0 });
    expect(r.emittedBytes).toBe(100);
  });

  it("drops a breakdown whose sums exceed the totals, keeping consistent ones", () => {
    const r = sanitizeImpact({
      ...base,
      commands: 10,
      raw_bytes: 1_000,
      emitted_bytes: 100,
      bytes_saved: 900,
      tokens_saved: 225,
      programs: {
        git: { runs: 10, raw_bytes: 50_000, emitted_bytes: 100, bytes_saved: 49_000, tokens_saved: 12_250 },
      },
      agents: {
        claude: { runs: 10, raw_bytes: 1_000, emitted_bytes: 100, bytes_saved: 900, tokens_saved: 225 },
      },
    });
    expect(r.programs).toEqual([]);
    expect(r.flags).toContain("programs_over_totals");
    expect(r.agents).toHaveLength(1);
    expect(r.flags).not.toContain("agents_over_totals");
  });

  it("bounds each breakdown entry's saved by its own raw, not raw minus emitted", () => {
    const r = sanitizeImpact({
      ...base,
      commands: 10,
      raw_bytes: 10_000,
      emitted_bytes: 100,
      bytes_saved: 9_900,
      tokens_saved: 2_475,
      programs: {
        // on_empty-shaped entry: saved exceeds raw - emitted but not raw, so it
        // is kept whole; a claim beyond raw would be clamped to raw.
        git: { runs: 1, raw_bytes: 100, emitted_bytes: 90, bytes_saved: 100, tokens_saved: 25 },
        rg: { runs: 1, raw_bytes: 50, emitted_bytes: 0, bytes_saved: 9_999, tokens_saved: 25 },
      },
    });
    const byName = Object.fromEntries(r.programs.map((p) => [p.name, p]));
    expect(byName.git?.bytesSaved).toBe(100);
    expect(byName.rg?.bytesSaved).toBe(50);
  });

  it("keeps a breakdown whose per-entry token rounding sums past the total", () => {
    // 50 programs x 1 byte saved: the client rounds each entry up to 1 token
    // while the total rounds to 13. The per-entry slack must absorb this.
    const programs: Record<string, object> = {};
    for (let i = 0; i < 50; i++) {
      programs[`p${i}`] = { runs: 1, raw_bytes: 1, emitted_bytes: 0, bytes_saved: 1, tokens_saved: 1 };
    }
    const r = sanitizeImpact({
      ...base,
      commands: 50,
      raw_bytes: 50,
      emitted_bytes: 0,
      bytes_saved: 50,
      tokens_saved: 13,
      programs,
    });
    expect(r.programs).toHaveLength(50);
    expect(r.flags).toEqual([]);
  });
});
