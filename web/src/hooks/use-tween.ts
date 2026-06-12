import { useEffect, useRef, useState } from "react";

const DEFAULT_DURATION = 1600;

const easeOutCubic = (p: number) => 1 - (1 - p) ** 3;

const prefersReducedMotion = (): boolean => {
  return (
    typeof window !== "undefined" &&
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches
  );
};

export const useTween = (
  target: number,
  duration = DEFAULT_DURATION
): number => {
  const [display, setDisplay] = useState(target);
  const refDisplay = useRef(target);
  refDisplay.current = display;
  const refRaf = useRef<number | null>(null);

  useEffect(() => {
    const from = refDisplay.current;
    if (target === from) {
      return;
    }

    if (duration <= 0 || prefersReducedMotion()) {
      setDisplay(target);
      return;
    }

    const delta = target - from;
    let start: number | null = null;

    const tick = (now: number) => {
      if (start === null) {
        start = now;
      }
      const p = Math.min((now - start) / duration, 1);
      setDisplay(p >= 1 ? target : from + delta * easeOutCubic(p));
      if (p < 1) {
        refRaf.current = requestAnimationFrame(tick);
      }
    };

    refRaf.current = requestAnimationFrame(tick);

    return () => {
      if (refRaf.current !== null) {
        cancelAnimationFrame(refRaf.current);
      }
    };
  }, [target, duration]);

  return display;
};
