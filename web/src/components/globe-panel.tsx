import createGlobe, { type Arc, type Globe, type Marker } from "cobe";
import { motion, useReducedMotion } from "motion/react";
import {
  type CSSProperties,
  type PointerEvent as ReactPointerEvent,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import { countryMeta, TOKEN_PRICE_PER_M } from "../data";
import {
  countryCode,
  flagEmoji,
  formatBytes,
  formatCompact,
  formatInt,
  formatTokens,
  formatUsd,
  topCountries,
} from "../format";
import { fadeUp, fadeUpSmall, staggerContainer } from "../lib/variants";
import type { ImpactStats } from "../types";

// Canonical cobe focus angles: rotate so a marker faces the camera.
function focusAngles(lat: number, lng: number): { phi: number; theta: number } {
  return {
    phi: Math.PI - ((lng * Math.PI) / 180 - Math.PI / 2),
    theta: Math.max(-1, Math.min(1, (lat * Math.PI) / 180)),
  };
}

function shortestAngle(delta: number): number {
  const twoPi = Math.PI * 2;
  return ((((delta + Math.PI) % twoPi) + twoPi) % twoPi) - Math.PI;
}

// cobe exposes marker positions as CSS anchors (--cobe-<id>) for DOM overlays;
// anchor positioning is Chromium-only, so tooltips degrade to nothing elsewhere.
const ANCHOR_OK =
  typeof CSS !== "undefined" &&
  typeof CSS.supports === "function" &&
  CSS.supports("anchor-name: --x");

type CountryRow = {
  rank: number;
  code: string;
  name: string;
  location: [number, number];
  saved: number;
  tokens: number;
  commands: number;
  size: number;
};

export function GlobePanel({ stats }: { stats: ImpactStats }) {
  const rows = buildRows(stats);
  const totals = stats.totals || {};
  const reduce = useReducedMotion();
  const [focus, setFocus] = useState<CountryRow | null>(null);

  return (
    <section
      aria-label="Global ctx-wire impact"
      className="globe-card-bg grid grid-cols-1 items-center gap-globegap rounded-section p-cardpad lg:grid-cols-2"
    >
      <motion.div
        variants={reduce ? undefined : staggerContainer}
        initial={reduce ? undefined : "hidden"}
        whileInView="visible"
        viewport={{ once: true, amount: 0.2 }}
        className="relative z-10"
      >
        <motion.p
          variants={reduce ? undefined : fadeUp}
          className="m-0 mb-4 inline-flex items-center gap-2.5 font-mono text-xs font-medium uppercase tracking-eyebrow text-green"
        >
          <span className="size-1.5 rounded-full bg-green shadow-dot" />
          global reach
        </motion.p>

        <motion.h2
          variants={reduce ? undefined : fadeUp}
          className="m-0 mb-6 font-display text-h2 font-extrabold text-head"
        >
          Saved context,
          <br />
          <span className="text-green">everywhere</span>.
        </motion.h2>

        <motion.div
          variants={reduce ? undefined : fadeUp}
          className="mb-7 border-b border-line-soft pb-6"
        >
          <div className="flex flex-wrap gap-reachgap">
            <ReachStat label="Countries" value={formatInt(rows.length)} />
            <ReachStat
              label="Commands filtered"
              value={formatCompact(Number(totals.commands || 0))}
            />
            <ReachStat
              label="Tokens saved"
              value={formatTokens(Number(totals.tokens_saved || 0))}
            />
            <ReachStat
              label="$ saved · est."
              value={formatUsd(
                (Number(totals.tokens_saved || 0) / 1_000_000) *
                  TOKEN_PRICE_PER_M
              )}
            />
          </div>
          <p className="m-0 mt-3 font-mono text-2xs text-label">
            Dollar figures estimate input tokens at ${TOKEN_PRICE_PER_M}/1M;
            actual varies by model.
          </p>
        </motion.div>

        <CountryPills
          rows={rows.slice(0, 10)}
          activeCode={focus?.code ?? null}
          onSelect={setFocus}
        />
      </motion.div>

      <div className="relative mx-auto grid aspect-square w-full max-w-scope place-items-center">
        <div className="scope-frame">
          <span className="scope-tick tl" />
          <span className="scope-tick tr" />
          <span className="scope-tick bl" />
          <span className="scope-tick br" />
        </div>
        <div
          className="scope-ring r1 motion-safe:animate-spin-slow"
          aria-hidden="true"
        />
        <div
          className="scope-ring r2 motion-safe:animate-spin-rev"
          aria-hidden="true"
        />
        <div
          className="scope-sweep motion-safe:animate-sweep"
          aria-hidden="true"
        />
        <GlobePulse
          rows={rows}
          focus={focus}
          onUserRotate={() => setFocus(null)}
        />
        <div className="readout-glass absolute bottom-2 left-1/2 inline-flex -translate-x-1/2 items-center gap-2 rounded-full px-3 py-1 font-mono text-2xs tracking-wide text-label">
          <span className="readout-dot size-1.5 rounded-full bg-green motion-safe:animate-pulse-dot" />
          <span className="text-green">{rows.length}</span>{" "}
          {rows.length === 1 ? "country" : "countries"} reporting
        </div>
      </div>
    </section>
  );
}

function ReachStat({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-1">
      <span className="font-mono text-reach font-bold tabular-nums text-head">
        {value}
      </span>
      <span className="font-mono text-2xs uppercase tracking-widest text-label">
        {label}
      </span>
    </div>
  );
}

function CountryPills({
  activeCode,
  onSelect,
  rows,
}: {
  activeCode: string | null;
  onSelect: (row: CountryRow | null) => void;
  rows: CountryRow[];
}) {
  const reduce = useReducedMotion();
  return (
    <motion.ul
      variants={reduce ? undefined : staggerContainer}
      initial={reduce ? undefined : "hidden"}
      whileInView="visible"
      viewport={{ once: true, amount: 0.1 }}
      aria-label="Countries by context saved"
      className="m-0 flex list-none flex-wrap gap-2 p-0"
    >
      {rows.map((row) => {
        const active = row.code === activeCode;
        return (
          <motion.li
            key={row.code}
            variants={reduce ? undefined : fadeUpSmall}
            className="m-0"
          >
            <button
              type="button"
              onClick={() => onSelect(active ? null : row)}
              title={
                active
                  ? `Release ${row.name} and resume rotation`
                  : `Rotate the globe to ${row.name}`
              }
              aria-pressed={active}
              className={`inline-flex cursor-pointer items-center gap-2 rounded-full px-3 py-1.5 font-mono text-2xs ring-1 ring-inset transition-colors ${
                active
                  ? "bg-green/10 ring-green/40"
                  : "bg-white/3 ring-line hover:bg-white/6"
              }`}
            >
              <span aria-hidden="true" className="text-sm leading-none">
                {flagEmoji(row.code)}
              </span>
              <span className={active ? "text-head" : "text-fg"}>
                {row.name}
              </span>
              <span className="tabular-nums text-cyan">
                {formatTokens(row.tokens)}
              </span>
              <span className="tabular-nums text-label">
                {formatBytes(row.saved)}
              </span>
            </button>
          </motion.li>
        );
      })}
    </motion.ul>
  );
}

function GlobePulse({
  focus,
  onUserRotate,
  rows,
  speed = 0.0028,
}: {
  focus: CountryRow | null;
  onUserRotate: () => void;
  rows: CountryRow[];
  speed?: number;
}) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const globeRef = useRef<Globe | null>(null);
  const markersRef = useRef<Marker[]>(rows.map(toCobeMarker));
  const arcsRef = useRef<Arc[]>(buildArcs(rows));
  const pointerInteracting = useRef<{ x: number; y: number } | null>(null);
  const dragOffset = useRef({ phi: 0, theta: 0 });
  const phiOffsetRef = useRef(0);
  const thetaOffsetRef = useRef(0);
  const pausedRef = useRef(false);
  const focusRef = useRef<{ phi: number; theta: number } | null>(null);

  useEffect(() => {
    if (!focus) {
      focusRef.current = null;
      return;
    }
    focusRef.current = focusAngles(focus.location[0], focus.location[1]);
  }, [focus]);

  useEffect(() => {
    markersRef.current = rows.map(toCobeMarker);
    arcsRef.current = buildArcs(rows);
    globeRef.current?.update({
      markers: markersRef.current,
      arcs: arcsRef.current,
    });
  }, [rows]);

  const handlePointerDown = useCallback(
    (event: ReactPointerEvent<HTMLCanvasElement>) => {
      focusRef.current = null;
      onUserRotate();
      pointerInteracting.current = { x: event.clientX, y: event.clientY };
      dragOffset.current = { phi: 0, theta: 0 };
      pausedRef.current = true;
      event.currentTarget.style.cursor = "grabbing";
    },
    [onUserRotate]
  );

  const handlePointerUp = useCallback(() => {
    if (pointerInteracting.current) {
      phiOffsetRef.current += dragOffset.current.phi;
      thetaOffsetRef.current += dragOffset.current.theta;
      dragOffset.current = { phi: 0, theta: 0 };
    }
    pointerInteracting.current = null;
    pausedRef.current = false;
    if (canvasRef.current) canvasRef.current.style.cursor = "grab";
  }, []);

  useEffect(() => {
    const handlePointerMove = (event: PointerEvent) => {
      if (!pointerInteracting.current) return;
      dragOffset.current = {
        phi: (event.clientX - pointerInteracting.current.x) / 280,
        theta: (event.clientY - pointerInteracting.current.y) / 900,
      };
    };

    window.addEventListener("pointermove", handlePointerMove, {
      passive: true,
    });
    window.addEventListener("pointerup", handlePointerUp, { passive: true });
    return () => {
      window.removeEventListener("pointermove", handlePointerMove);
      window.removeEventListener("pointerup", handlePointerUp);
    };
  }, [handlePointerUp]);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    let frame = 0;
    let phi = -0.52;
    let activeSize = 0;

    const create = (size: number) => {
      if (size < 120) return;
      globeRef.current?.destroy();
      activeSize = size;
      canvas.width = size * Math.min(window.devicePixelRatio || 1, 2);
      canvas.height = size * Math.min(window.devicePixelRatio || 1, 2);
      canvas.style.opacity = "0";

      globeRef.current = createGlobe(canvas, {
        devicePixelRatio: Math.min(window.devicePixelRatio || 1, 2),
        width: canvas.width,
        height: canvas.height,
        phi,
        theta: 0.22,
        dark: 1,
        diffuse: 1.35,
        mapSamples: 21000,
        mapBrightness: 7.2,
        mapBaseBrightness: 0.12,
        baseColor: [0.22, 0.4, 0.32],
        markerColor: [0.56, 0.94, 0.48],
        glowColor: [0.06, 0.16, 0.12],
        markerElevation: 0.02,
        scale: 1,
        opacity: 0.95,
        markers: markersRef.current,
        arcs: arcsRef.current,
        arcColor: [0.38, 0.92, 0.78],
        arcWidth: 0.5,
        arcHeight: 0.42,
      });

      // Reveal a frame past cobe's first paint (it has no onRender hook), so the
      // canvas never flashes its blank backing buffer on load.
      requestAnimationFrame(() =>
        requestAnimationFrame(() => {
          canvas.style.opacity = "1";
        })
      );
    };

    const prefersReducedMotion =
      typeof window !== "undefined" &&
      typeof window.matchMedia === "function" &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches;

    const animate = () => {
      const target = focusRef.current;
      if (target) {
        // Ease the offsets so the picked country rotates to face the camera.
        thetaOffsetRef.current +=
          (target.theta - 0.22 - thetaOffsetRef.current) * 0.08;
        const desired =
          phiOffsetRef.current +
          shortestAngle(target.phi - phi - phiOffsetRef.current);
        phiOffsetRef.current += (desired - phiOffsetRef.current) * 0.08;
      } else if (!pausedRef.current && !prefersReducedMotion) {
        // Honor prefers-reduced-motion: hold the globe still rather than
        // auto-rotating it. A direct drag still updates phi via dragOffset, so
        // the globe stays interactive without continuous, unrequested motion.
        phi += speed;
      }
      globeRef.current?.update({
        phi: phi + phiOffsetRef.current + dragOffset.current.phi,
        theta: 0.22 + thetaOffsetRef.current + dragOffset.current.theta,
      });
      frame = requestAnimationFrame(animate);
    };

    const resize = () => {
      const rect = canvas.getBoundingClientRect();
      const size = Math.round(Math.min(rect.width, rect.height));
      if (!globeRef.current || Math.abs(size - activeSize) > 12) create(size);
    };

    const observer = new ResizeObserver(resize);
    observer.observe(canvas);
    resize();
    animate();

    return () => {
      observer.disconnect();
      cancelAnimationFrame(frame);
      globeRef.current?.destroy();
      globeRef.current = null;
    };
  }, [speed]);

  return (
    <div className="relative z-10 aspect-square w-5/6 select-none">
      <canvas
        ref={canvasRef}
        className="globe-canvas"
        onPointerDown={handlePointerDown}
        aria-label="Interactive ctx-wire impact globe"
      />
      {ANCHOR_OK
        ? rows.map((row) => {
            const id = row.code.toLowerCase();
            return (
              <div
                key={row.code}
                className={`globe-marker ${
                  focus?.code === row.code ? "is-focused" : ""
                }`}
                style={
                  {
                    positionAnchor: `--cobe-${id}`,
                    "--vis": `var(--cobe-visible-${id}, 0)`,
                  } as CSSProperties
                }
              >
                <span className="globe-beacon" aria-hidden="true" />
                <span className="globe-tooltip">
                  <span aria-hidden="true" className="mr-1">
                    {flagEmoji(row.code)}
                  </span>
                  {row.name}
                </span>
              </div>
            );
          })
        : null}
    </div>
  );
}

function buildRows(stats: ImpactStats): CountryRow[] {
  return topCountries(stats)
    .map((country, index) => {
      const code = countryCode(country);
      const meta = countryMeta[code];
      if (!meta) return null;
      const saved = Number(country.bytes_saved || 0);
      return {
        rank: index + 1,
        code,
        name: meta.name,
        location: [meta.lat, meta.lng] as [number, number],
        saved,
        tokens: Number(country.tokens_saved || 0),
        commands: Number(country.commands || 0),
        // Faint center point only; the visible marker is the DOM beacon ring.
        size: 0.012,
      };
    })
    .filter((r): r is CountryRow => Boolean(r));
}

function toCobeMarker(row: CountryRow): Marker {
  return {
    id: row.code.toLowerCase(),
    location: row.location,
    size: row.size,
  };
}

function buildArcs(rows: CountryRow[]): Arc[] {
  if (rows.length < 2) return [];
  const nodes = rows.slice(0, 6);
  const arcs: Arc[] = [];
  for (let i = 0; i < nodes.length - 1; i++) {
    arcs.push({ from: nodes[i].location, to: nodes[i + 1].location });
  }
  arcs.push({ from: nodes[nodes.length - 1].location, to: nodes[0].location });
  return arcs;
}
