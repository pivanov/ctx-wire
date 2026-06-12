import { IconStarFilled } from "@tabler/icons-react";
import { motion, useReducedMotion } from "motion/react";
import { formatInt } from "../format";
import type { TStargazer } from "../hooks/use-community";
import { fadeUp, staggerContainer } from "../lib/variants";
import { SectionEyebrow } from "./section-heading";

const REPO = "https://github.com/pivanov/ctx-wire";
const MAX_SHOWN = 28;

export const Stargazers = ({
  stargazers,
  stars,
}: {
  stargazers: TStargazer[];
  stars: number;
}) => {
  const reduce = useReducedMotion();
  const count = Math.max(stars, stargazers.length);
  const shown = stargazers.slice(0, MAX_SHOWN);
  const extra = count - shown.length;
  const hasFaces = shown.length > 0;

  return (
    <motion.section
      aria-label="GitHub stargazers"
      variants={reduce ? undefined : staggerContainer}
      initial={reduce ? undefined : "hidden"}
      whileInView="visible"
      viewport={{ once: true, amount: 0.1 }}
      className="globe-card-bg flex w-full max-w-stage flex-col items-center gap-6 rounded-section p-cardpad text-center"
    >
      <SectionEyebrow
        icon={<IconStarFilled size={13} className="text-yellow" />}
      >
        community
      </SectionEyebrow>

      <motion.h2
        variants={reduce ? undefined : fadeUp}
        className="m-0 max-w-3xl font-display text-h2 font-extrabold text-head"
      >
        {hasFaces ? (
          <>
            Starred by <span className="text-green">{formatInt(count)}</span>{" "}
            {count === 1 ? "developer" : "developers"}.
          </>
        ) : (
          <>
            Be the first to <span className="text-green">star</span> ctx-wire.
          </>
        )}
      </motion.h2>

      {hasFaces ? (
        <motion.div
          variants={reduce ? undefined : fadeUp}
          className="flex max-w-2xl flex-wrap items-center justify-center"
        >
          {shown.map((s) => (
            <a
              key={s.login}
              href={s.url}
              target="_blank"
              rel="noreferrer"
              title={s.login}
              className="-ml-2 transition-transform first:ml-0 hover:z-10 hover:-translate-y-1"
            >
              <img
                src={s.avatar}
                alt={s.login}
                loading="lazy"
                referrerPolicy="no-referrer"
                className="size-10 rounded-full bg-panel ring-2 ring-bg"
              />
            </a>
          ))}
          {extra > 0 ? (
            <span className="-ml-2 grid size-10 place-items-center rounded-full bg-green/10 font-mono text-2xs text-green ring-2 ring-bg">
              +{formatInt(extra)}
            </span>
          ) : null}
        </motion.div>
      ) : (
        <motion.p
          variants={reduce ? undefined : fadeUp}
          className="m-0 max-w-md font-mono text-sub leading-relaxed text-label"
        >
          If ctx-wire saved your agent some tokens, a star helps other
          developers find it.
        </motion.p>
      )}

      <motion.a
        variants={reduce ? undefined : fadeUp}
        whileHover={reduce ? undefined : { y: -1 }}
        whileTap={reduce ? undefined : { scale: 0.97 }}
        href={`${REPO}/stargazers`}
        target="_blank"
        rel="noreferrer"
        className="inline-flex items-center gap-2 rounded-full bg-green px-5 py-2.5 font-mono text-sm font-bold text-ink shadow-badge transition-colors hover:bg-teal"
      >
        <IconStarFilled size={15} />
        Star on GitHub
      </motion.a>
    </motion.section>
  );
};
