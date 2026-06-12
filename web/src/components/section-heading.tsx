import { motion, useReducedMotion } from "motion/react";
import type { ReactNode } from "react";
import { fadeUp } from "../lib/variants";

// The two sanctioned section-heading tiers. SectionEyebrow (dot or icon + caps)
// opens the full-width card sections; SectionKicker (line-prefix label with an
// optional inline descriptor) opens the narrower strip sections. Use one of
// these; do not fork a third pattern.

export const SectionEyebrow = ({
  children,
  className,
  icon,
}: {
  children: ReactNode;
  className?: string;
  icon?: ReactNode;
}) => {
  const reduce = useReducedMotion();
  return (
    <motion.p
      variants={reduce ? undefined : fadeUp}
      className={`m-0 inline-flex items-center gap-2.5 font-mono text-xs font-medium uppercase tracking-eyebrow text-green${className ? ` ${className}` : ""}`}
    >
      {icon ?? <span className="size-1.5 rounded-full bg-green shadow-dot" />}
      {children}
    </motion.p>
  );
};

export const SectionKicker = ({
  children,
  desc,
}: {
  children: ReactNode;
  desc?: string;
}) => {
  return (
    <div className="flex items-baseline gap-4">
      <span className="relative pl-6 font-mono text-xs font-semibold uppercase tracking-kicker text-green before:absolute before:left-0 before:top-1/2 before:h-px before:w-3.5 before:bg-green">
        {children}
      </span>
      {desc ? (
        <p className="m-0 hidden font-mono text-xs text-label sm:block">
          {desc}
        </p>
      ) : null}
    </div>
  );
};
