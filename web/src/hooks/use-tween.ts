import { useEffect, useRef, useState } from "react";

const DEFAULT_DURATION = 1600;

const easeOutCubic = (p: number) => 1 - (1 - p) ** 3;

function prefersReducedMotion(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches
  );
}

export function useTween(target: number, duration = DEFAULT_DURATION): number {
  const [display, setDisplay] = useState(target);
  const displayRef = useRef(target);
  displayRef.current = display;
  const rafRef = useRef<number | null>(null);

  useEffect(() => {
    const from = displayRef.current;
    if (target === from) return;

    if (duration <= 0 || prefersReducedMotion()) {
      setDisplay(target);
      return;
    }

    const delta = target - from;
    let start: number | null = null;

    const tick = (now: number) => {
      if (start === null) start = now;
      const p = Math.min((now - start) / duration, 1);
      setDisplay(p >= 1 ? target : from + delta * easeOutCubic(p));
      if (p < 1) rafRef.current = requestAnimationFrame(tick);
    };

    rafRef.current = requestAnimationFrame(tick);

    return () => {
      if (rafRef.current !== null) cancelAnimationFrame(rafRef.current);
    };
  }, [target, duration]);

  return display;
}
