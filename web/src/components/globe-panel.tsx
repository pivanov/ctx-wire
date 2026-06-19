import createGlobe, { type Globe, type Marker } from "cobe";
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
  formatCompact3,
  formatInt,
  formatTokens,
  formatTokens3,
  formatUsdCents,
  topCountries,
} from "../format";
import { fadeUp, staggerContainer } from "../lib/variants";
import type { TImpactStats } from "../types";
import { AnimatedNumber } from "./animated-number";
import { SectionEyebrow } from "./section-heading";

// Canonical cobe focus angles: rotate so a marker faces the camera.
const focusAngles = (
  lat: number,
  lng: number
): { phi: number; theta: number } => {
  return {
    phi: Math.PI - ((lng * Math.PI) / 180 - Math.PI / 2),
    theta: Math.max(-1, Math.min(1, (lat * Math.PI) / 180)),
  };
};

const shortestAngle = (delta: number): number => {
  const twoPi = Math.PI * 2;
  return ((((delta + Math.PI) % twoPi) + twoPi) % twoPi) - Math.PI;
};

// cobe exposes marker positions as CSS anchors (--cobe-<id>) for DOM overlays;
// anchor positioning is Chromium-only, so tooltips degrade to nothing elsewhere.
const ANCHOR_OK =
  typeof CSS !== "undefined" &&
  typeof CSS.supports === "function" &&
  CSS.supports("anchor-name: --x");

type TCountryRow = {
  rank: number;
  code: string;
  name: string;
  location: [number, number];
  saved: number;
  tokens: number;
  commands: number;
  size: number;
  // Beacon scale relative to the top country (sqrt of share, so the tail stays
  // visible). The globe dots encode impact, not just presence.
  weight: number;
};

export const GlobePanel = ({ stats }: { stats: TImpactStats }) => {
  const rows = buildRows(stats);
  const totals = stats.totals || {};
  const live = Number(totals.commands || 0) > 0;
  const reduce = useReducedMotion();
  const [focus, setFocus] = useState<TCountryRow | null>(null);
  const [globeActive, setGlobeActive] = useState(false);
  const refGlobeWrapper = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const el = refGlobeWrapper.current;
    if (!el) {
      return;
    }
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          setGlobeActive(true);
          observer.disconnect();
        }
      },
      { rootMargin: "200px", threshold: 0 }
    );
    observer.observe(el);
    return () => {
      observer.disconnect();
    };
  }, []);

  return (
    <section
      aria-label="Global ctx-wire impact"
      className="globe-card-bg grid grid-cols-1 items-center gap-globegap rounded-section p-cardpad lg:grid-cols-2"
    >
      <motion.div
        variants={reduce ? undefined : staggerContainer}
        initial={reduce ? undefined : "hidden"}
        whileInView="visible"
        viewport={{ once: true, amount: 0.1 }}
        className="relative z-10"
      >
        <SectionEyebrow className="mb-4">global reach</SectionEyebrow>

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
          <div className="grid grid-cols-2 gap-x-reachgap gap-y-6">
            <ReachStat
              label="Countries"
              value={rows.length}
              format={formatInt}
              live={live}
            />
            <ReachStat
              label="Commands filtered"
              value={Number(totals.commands || 0)}
              format={formatCompact3}
              live={live}
            />
            <ReachStat
              label="Tokens saved"
              value={Number(totals.tokens_saved || 0)}
              format={formatTokens3}
              live={live}
            />
            <ReachStat
              label="$ saved · est."
              value={
                (Number(totals.tokens_saved || 0) / 1_000_000) *
                TOKEN_PRICE_PER_M
              }
              format={formatUsdCents}
              live={live}
            />
          </div>
          <p className="m-0 mt-3 font-mono text-2xs text-label">
            Dollar figures estimate input tokens at ${TOKEN_PRICE_PER_M}/1M;
            actual varies by model.
          </p>
        </motion.div>

        <CountryPills
          rows={rows}
          activeCode={focus?.code ?? null}
          onSelect={setFocus}
        />
      </motion.div>

      <div
        ref={refGlobeWrapper}
        className="relative mx-auto grid aspect-square w-full max-w-scope place-items-center"
      >
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
        {globeActive ? (
          <GlobePulse
            rows={rows}
            focus={focus}
            onUserRotate={() => setFocus(null)}
          />
        ) : (
          <div
            className="relative z-10 aspect-square w-5/6"
            aria-hidden="true"
          />
        )}
        <div className="readout-glass absolute bottom-2 left-1/2 inline-flex -translate-x-1/2 items-center gap-2 rounded-full px-3 py-1 font-mono text-2xs tracking-wide text-label">
          <span className="readout-dot size-1.5 rounded-full bg-green motion-safe:animate-pulse-dot" />
          {live ? (
            <>
              <span className="text-green">{rows.length}</span>{" "}
              {rows.length === 1 ? "country" : "countries"} reporting
            </>
          ) : (
            "acquiring signal"
          )}
        </div>
      </div>
    </section>
  );
};

const ReachStat = ({
  format,
  label,
  live,
  value,
}: {
  format: (n: number) => string;
  label: string;
  live: boolean;
  value: number;
}) => {
  return (
    <div className="flex flex-col gap-1">
      {live ? (
        <AnimatedNumber
          value={value}
          format={format}
          className="font-mono text-reach font-bold tabular-nums text-head"
        />
      ) : (
        <span
          className="shimmer h-[1em] w-20 self-start font-mono text-reach"
          aria-hidden="true"
        />
      )}
      <span className="font-mono text-2xs uppercase tracking-widest text-label">
        {label}
      </span>
    </div>
  );
};

const CountryPills = ({
  activeCode,
  onSelect,
  rows,
}: {
  activeCode: string | null;
  onSelect: (row: TCountryRow | null) => void;
  rows: TCountryRow[];
}) => {
  const reduce = useReducedMotion();
  return (
    <motion.ul
      variants={reduce ? undefined : fadeUp}
      aria-label="Countries by context saved"
      className="m-0 flex list-none flex-wrap gap-2 p-0"
    >
      {rows.map((row) => {
        const active = row.code === activeCode;
        return (
          <li key={row.code} className="m-0">
            <button
              type="button"
              onClick={() => onSelect(active ? null : row)}
              title={
                active
                  ? `Release ${row.name} and resume rotation`
                  : `Rotate the globe to ${row.name}`
              }
              aria-pressed={active}
              className={`inline-flex cursor-pointer items-center gap-2 rounded-full px-3 py-1.5 font-mono text-2xs ring-1 ring-inset transition-[background-color,transform] duration-150 ease-out motion-safe:active:scale-[0.97] ${
                active
                  ? "bg-green/10 ring-green/40"
                  : "bg-white/3 ring-line hover:bg-white/6"
              }`}
            >
              <span aria-hidden="true" className="text-sm leading-none">
                {flagEmoji(row.code)}
              </span>
              <span className={active ? "text-head" : "text-fg"}>
                {row.code.toUpperCase()}
              </span>
              <span className="tabular-nums text-cyan">
                {formatTokens(row.tokens)}
              </span>
              <span className="tabular-nums text-label">
                {formatBytes(row.saved)}
              </span>
            </button>
          </li>
        );
      })}
    </motion.ul>
  );
};

const GlobePulse = ({
  focus,
  onUserRotate,
  rows,
  speed = 0.0028,
}: {
  focus: TCountryRow | null;
  onUserRotate: () => void;
  rows: TCountryRow[];
  speed?: number;
}) => {
  const refCanvas = useRef<HTMLCanvasElement | null>(null);
  const refGlobe = useRef<Globe | null>(null);
  const refMarkers = useRef<Marker[]>(rows.map(toCobeMarker));
  const refPointerInteracting = useRef<{ x: number; y: number } | null>(null);
  const refDragOffset = useRef({ phi: 0, theta: 0 });
  const refPhiOffset = useRef(0);
  const refThetaOffset = useRef(0);
  const refPaused = useRef(false);
  const refFocus = useRef<{ phi: number; theta: number } | null>(null);
  const refOnScreen = useRef(true);

  useEffect(() => {
    if (!focus) {
      refFocus.current = null;
      return;
    }
    refFocus.current = focusAngles(focus.location[0], focus.location[1]);
  }, [focus]);

  useEffect(() => {
    refMarkers.current = rows.map(toCobeMarker);
    refGlobe.current?.update({
      markers: refMarkers.current,
    });
  }, [rows]);

  const handlePointerDown = useCallback(
    (event: ReactPointerEvent<HTMLCanvasElement>) => {
      refFocus.current = null;
      onUserRotate();
      refPointerInteracting.current = { x: event.clientX, y: event.clientY };
      refDragOffset.current = { phi: 0, theta: 0 };
      refPaused.current = true;
      event.currentTarget.style.cursor = "grabbing";
    },
    [onUserRotate]
  );

  const handlePointerUp = useCallback(() => {
    if (refPointerInteracting.current) {
      refPhiOffset.current += refDragOffset.current.phi;
      refThetaOffset.current += refDragOffset.current.theta;
      refDragOffset.current = { phi: 0, theta: 0 };
    }
    refPointerInteracting.current = null;
    refPaused.current = false;
    if (refCanvas.current) {
      refCanvas.current.style.cursor = "grab";
    }
  }, []);

  useEffect(() => {
    const handlePointerMove = (event: PointerEvent) => {
      if (!refPointerInteracting.current) {
        return;
      }
      refDragOffset.current = {
        phi: (event.clientX - refPointerInteracting.current.x) / 280,
        theta: (event.clientY - refPointerInteracting.current.y) / 900,
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
    const canvas = refCanvas.current;
    if (!canvas) {
      return;
    }

    let frame = 0;
    let phi = -0.52;
    let activeSize = 0;
    let painted = 0;
    let revealed = false;

    const create = (size: number) => {
      if (size < 120) {
        return;
      }
      refGlobe.current?.destroy();
      activeSize = size;
      canvas.width = size * Math.min(window.devicePixelRatio || 1, 2);
      canvas.height = size * Math.min(window.devicePixelRatio || 1, 2);
      canvas.style.opacity = "0";

      refGlobe.current = createGlobe(canvas, {
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
        markers: refMarkers.current,
        arcs: [],
      });

      // Reveal only after the globe has actually painted a few frames (cobe has
      // no onRender hook here). The animate loop flips the opacity once `painted`
      // crosses the threshold, so the fade-in never shows a blank/mid-render
      // globe. Reset both so a resize-driven recreate re-reveals the same way.
      revealed = false;
      painted = 0;
    };

    const prefersReducedMotion =
      typeof window !== "undefined" &&
      typeof window.matchMedia === "function" &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches;

    const animate = () => {
      const target = refFocus.current;
      if (target) {
        // Ease the offsets so the picked country rotates to face the camera.
        refThetaOffset.current +=
          (target.theta - 0.22 - refThetaOffset.current) * 0.08;
        const desired =
          refPhiOffset.current +
          shortestAngle(target.phi - phi - refPhiOffset.current);
        refPhiOffset.current += (desired - refPhiOffset.current) * 0.08;
      } else if (
        !refPaused.current &&
        !prefersReducedMotion &&
        refOnScreen.current
      ) {
        // Honor prefers-reduced-motion: hold the globe still rather than
        // auto-rotating it. A direct drag still updates phi via refDragOffset, so
        // the globe stays interactive without continuous, unrequested motion.
        phi += speed;
      }
      // Skip the WebGL draw while the globe is scrolled off-screen: the loop
      // keeps running (so it always resumes) but the GPU stays idle.
      if (refOnScreen.current) {
        refGlobe.current?.update({
          phi: phi + refPhiOffset.current + refDragOffset.current.phi,
          theta: 0.22 + refThetaOffset.current + refDragOffset.current.theta,
        });
        // Fade the canvas in once the globe has drawn a few real frames, not on a
        // guessed RAF count, so no blank/bright flash shows through on load.
        if (!revealed && ++painted >= 4) {
          revealed = true;
          canvas.style.opacity = "1";
        }
      }
      frame = requestAnimationFrame(animate);
    };

    const resize = () => {
      const rect = canvas.getBoundingClientRect();
      const size = Math.round(Math.min(rect.width, rect.height));
      if (!refGlobe.current || Math.abs(size - activeSize) > 12) {
        create(size);
      }
    };

    const observer = new ResizeObserver(resize);
    observer.observe(canvas);

    // Pause the GPU render loop when the globe scrolls out of view.
    const visibility = new IntersectionObserver(
      ([entry]) => {
        refOnScreen.current = entry.isIntersecting;
      },
      { threshold: 0 }
    );
    visibility.observe(canvas);

    resize();
    animate();

    return () => {
      observer.disconnect();
      visibility.disconnect();
      cancelAnimationFrame(frame);
      refGlobe.current?.destroy();
      refGlobe.current = null;
    };
  }, [speed]);

  return (
    <div className="relative z-10 aspect-square w-5/6 select-none">
      <canvas
        ref={refCanvas}
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
                    "--beacon-scale": row.weight,
                    "--beacon-delay": `${row.rank * -0.37}s`,
                  } as CSSProperties
                }
              >
                <span className="globe-beacon" aria-hidden="true" />
                <span className="globe-tooltip">
                  <span aria-hidden="true" className="mr-1">
                    {flagEmoji(row.code)}
                  </span>
                  {row.name}
                  <span className="ml-1.5 tabular-nums text-cyan">
                    {formatTokens(row.tokens)}
                  </span>
                  <span className="ml-1.5 tabular-nums text-label">
                    {formatBytes(row.saved)}
                  </span>
                </span>
              </div>
            );
          })
        : null}
      {/* Anchor positioning is Chromium-only; elsewhere the focused country's
          readout pins to the globe corner so pill clicks still show the data. */}
      {!ANCHOR_OK && focus ? (
        <div className="readout-glass absolute left-3 top-3 z-10 inline-flex items-center gap-2 rounded-full px-3 py-1.5 font-mono text-2xs text-fg">
          <span aria-hidden="true">{flagEmoji(focus.code)}</span>
          {focus.name}
          <span className="tabular-nums text-cyan">
            {formatTokens(focus.tokens)}
          </span>
          <span className="tabular-nums text-label">
            {formatBytes(focus.saved)}
          </span>
        </div>
      ) : null}
    </div>
  );
};

const buildRows = (stats: TImpactStats): TCountryRow[] => {
  const base = topCountries(stats)
    .map((country, index) => {
      const code = countryCode(country);
      const meta = countryMeta[code];
      if (!meta) {
        return null;
      }
      const saved = Number(country.bytes_saved || 0);
      // Skip countries with nothing saved: this panel is "by context saved", so a
      // country reporting 0 B is noise even if it ran a few commands.
      if (saved <= 0) {
        return null;
      }
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
        weight: 1,
      };
    })
    .filter((r): r is TCountryRow => Boolean(r));
  const maxSaved = Math.max(...base.map((r) => r.saved), 1);
  return base.map((r) => ({
    ...r,
    weight: 0.7 + 0.75 * Math.sqrt(r.saved / maxSaved),
  }));
};

const toCobeMarker = (row: TCountryRow): Marker => {
  return {
    id: row.code.toLowerCase(),
    location: row.location,
    size: row.size,
  };
};
