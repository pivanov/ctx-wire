import { RiGithubFill, RiTwitterXFill } from "@remixicon/react";
import { IconStarFilled } from "@tabler/icons-react";
import { motion, useReducedMotion } from "motion/react";
import { formatCompact, formatInt } from "../format";
import { useTween } from "../hooks/use-tween";
import { BrandMark } from "./brand-mark";

type TProps = {
  stars: number;
  live: boolean;
  version: number;
  installs: number;
  reports: number;
};

const TACTILE = {
  whileHover: { y: -1 },
  whileTap: { scale: 0.96 },
  transition: { type: "spring", stiffness: 400, damping: 17 },
} as const;

export const TopBar = ({ installs, live, reports, stars, version }: TProps) => {
  const starCount = useTween(Number(stars || 0));
  const installCount = useTween(Number(installs || 0));
  const reportCount = useTween(Number(reports || 0));
  const reduce = useReducedMotion();

  return (
    <motion.header
      initial={reduce ? undefined : { opacity: 0, y: -12 }}
      animate={{ opacity: 1, y: 0 }}
      transition={
        reduce ? undefined : { duration: 0.6, ease: [0.16, 1, 0.3, 1] }
      }
      className="sticky top-0 z-20 flex h-14 items-center justify-between gap-4 bg-bg/20 px-gutter backdrop-blur-xl backdrop-saturate-150"
    >
      <a
        href="#top"
        aria-label="ctx-wire home"
        className="flex items-center gap-2.5"
      >
        <BrandMark size={26} />
        <span className="font-mono text-sm font-bold tracking-tight text-fg">
          ctx-wire
        </span>
        <span className="hidden rounded-full bg-green/10 px-2 py-0.5 font-mono text-2xs uppercase tracking-wider text-green ring-1 ring-inset ring-green/25 sm:inline">
          gain
        </span>
      </a>

      <nav
        aria-label="Primary"
        className="flex items-center gap-2 font-mono text-cap"
      >
        {/* The stats pill renders only once telemetry is live. A visitor must
            never see "idle · installs 0": a loading state that reads as a dead
            project in the most prominent spot on the page. */}
        {live ? (
          <motion.div
            initial={reduce ? undefined : { opacity: 0, scale: 0.96 }}
            animate={{ opacity: 1, scale: 1 }}
            transition={
              reduce ? undefined : { duration: 0.4, ease: [0.16, 1, 0.3, 1] }
            }
            className="inline-flex items-center gap-2.5 rounded-full bg-white/3 px-3 py-1 ring-1 ring-inset ring-line-soft"
          >
            <span
              title="Live telemetry"
              className="relative inline-flex items-center gap-1.5 text-green"
            >
              {version > 0 && !reduce ? (
                <motion.span
                  key={version}
                  initial={{ opacity: 0.5, scale: 0.7 }}
                  animate={{ opacity: 0, scale: 2.4 }}
                  transition={{ duration: 1, ease: "easeOut" }}
                  className="pointer-events-none absolute -left-0.5 top-1/2 size-2 -translate-y-1/2 rounded-full ring-1 ring-green/50"
                />
              ) : null}
              <span className="size-1.5 animate-pulse-dot rounded-full bg-green" />
              live
            </span>

            <span className="hidden h-3 w-px bg-line-soft sm:block" />
            <span
              title="reported installs"
              className="hidden items-center gap-1.5 sm:inline-flex"
            >
              <span className="text-label">installs</span>
              <span className="text-fg">{formatInt(installCount)}</span>
            </span>
            <span
              title="gain reports submitted"
              className="hidden items-center gap-1.5 sm:inline-flex"
            >
              <span className="text-label">reports</span>
              <span className="text-fg">{formatInt(reportCount)}</span>
            </span>
          </motion.div>
        ) : null}

        <motion.a
          {...(reduce ? {} : TACTILE)}
          href="https://github.com/pivanov/ctx-wire/stargazers"
          target="_blank"
          rel="noreferrer"
          className="inline-flex items-center gap-1.5 rounded-full px-3 py-1.5 text-fg ring-1 ring-inset ring-line-soft transition-colors hover:bg-green/10 hover:text-white"
        >
          <IconStarFilled size={13} className="text-yellow" />
          {formatCompact(starCount)}
        </motion.a>

        <motion.a
          {...(reduce ? {} : TACTILE)}
          href="https://x.com/ctxwire"
          target="_blank"
          rel="noreferrer"
          aria-label="ctx-wire on X"
          className="inline-flex items-center rounded-full p-1.5 text-fg ring-1 ring-inset ring-line-soft transition-colors hover:bg-green/10 hover:text-white"
        >
          <RiTwitterXFill size={15} />
        </motion.a>

        <motion.a
          {...(reduce ? {} : TACTILE)}
          href="https://github.com/pivanov/ctx-wire"
          target="_blank"
          rel="noreferrer"
          className="inline-flex items-center gap-1.5 rounded-full bg-green/10 px-3 py-1.5 text-green ring-1 ring-inset ring-green/30 transition-colors hover:bg-green/20"
        >
          <RiGithubFill size={15} />
          <span className="hidden sm:inline">GitHub</span>
        </motion.a>
      </nav>
    </motion.header>
  );
};
