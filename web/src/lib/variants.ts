import type { Variants } from "motion/react";

const GLIDE = [0.16, 1, 0.3, 1] as const;
const EXPO = [0.22, 1, 0.36, 1] as const;

// The container stays visible; only children fade. A hidden container blanks
// the whole section on fast scroll until its reveal triggers.
export const staggerContainer: Variants = {
  hidden: { opacity: 1 },
  visible: {
    opacity: 1,
    transition: { staggerChildren: 0.05 },
  },
};

export const fadeUp: Variants = {
  hidden: { opacity: 0, y: 14 },
  visible: {
    opacity: 1,
    y: 0,
    transition: { duration: 0.45, ease: GLIDE },
  },
};

export const fadeUpSmall: Variants = {
  hidden: { opacity: 0, y: 10 },
  visible: {
    opacity: 1,
    y: 0,
    transition: { duration: 0.5, ease: GLIDE },
  },
};

export const scaleIn: Variants = {
  hidden: { opacity: 0, scale: 0.96 },
  visible: {
    opacity: 1,
    scale: 1,
    transition: { duration: 0.55, ease: GLIDE },
  },
};

export const lineGrow: Variants = {
  hidden: { scaleX: 0, opacity: 0 },
  visible: {
    opacity: 1,
    scaleX: 1,
    transition: { duration: 0.5, ease: EXPO },
  },
};

export const rowIn: Variants = {
  hidden: { opacity: 0, x: -8 },
  visible: {
    opacity: 1,
    x: 0,
    transition: { duration: 0.5, ease: EXPO },
  },
};
