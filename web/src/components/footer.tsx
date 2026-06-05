import { RiGithubFill } from "@remixicon/react";
import { IconCheck, IconCopy, IconStarFilled } from "@tabler/icons-react";
import { useCopy } from "../hooks/use-copy";
import { BrandMark } from "./brand-mark";

const REPO = "https://github.com/pivanov/ctx-wire";
const INSTALL = "curl -fsSL https://ctx-wire.dev/install.sh | sh";

const BACKERS = [
  { label: "LogicStar AI", href: "https://logicstar.ai" },
  { label: "SashiDo.io", href: "https://www.sashido.io" },
];

export function Footer() {
  const [copied, copy] = useCopy();

  return (
    <footer className="relative z-10 mt-flow border-t border-line-soft bg-linear-to-b from-transparent to-panel/60">
      <div className="mx-auto w-full max-w-stage px-gutter">
        <div className="grid gap-10 py-12 lg:grid-cols-2 lg:items-center">
          <div>
            <div className="flex items-center gap-2.5">
              <BrandMark size={30} />
              <span className="font-mono text-base font-bold tracking-tight text-fg">
                ctx-wire
              </span>
              <span className="rounded-full bg-green/10 px-2 py-0.5 font-mono text-2xs uppercase tracking-wider text-green ring-1 ring-inset ring-green/25">
                unleashed
              </span>
            </div>
            <p className="mt-4 max-w-copy font-mono text-cap leading-relaxed text-label">
              A small Go binary that sits between AI coding agents and the noisy
              command output they pay tokens to read. Secrets-safe, with the
              full log kept on disk.
            </p>
          </div>

          <div className="flex flex-col gap-3 lg:items-end">
            <span className="font-mono text-2xs uppercase tracking-caps text-dim">
              Get started
            </span>
            <button
              type="button"
              onClick={() => copy(INSTALL)}
              title="Click to copy"
              className="no-scrollbar group inline-flex max-w-full items-center gap-3 overflow-x-auto rounded-card bg-screen px-4 py-2.5 font-mono text-2xs text-fg ring-1 ring-inset ring-line-soft transition-colors hover:ring-green/40"
            >
              <span className="select-none text-green">$</span>
              <span className="whitespace-nowrap">{INSTALL}</span>
              {copied ? (
                <IconCheck size={14} className="shrink-0 text-green" />
              ) : (
                <IconCopy
                  size={14}
                  className="shrink-0 text-label transition-colors group-hover:text-green"
                />
              )}
            </button>
            <a
              href={`${REPO}/stargazers`}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1.5 rounded-full bg-green/10 px-3 py-1.5 font-mono text-2xs font-medium text-green ring-1 ring-inset ring-green/30 transition-colors hover:bg-green/20"
            >
              <IconStarFilled size={13} />
              Star on GitHub
            </a>
          </div>
        </div>

        <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-3 border-t border-line-soft py-6 font-mono text-2xs text-label">
          <span>© 2026 Pavel Ivanov · Released under MIT</span>

          <span className="inline-flex flex-wrap items-center gap-x-3 gap-y-1">
            <span className="uppercase tracking-caps text-dim">Backed by</span>
            {BACKERS.map((backer, index) => (
              <span
                key={backer.label}
                className="inline-flex items-center gap-3"
              >
                {index > 0 ? (
                  <span aria-hidden="true" className="text-dim">
                    ·
                  </span>
                ) : null}
                <a
                  href={backer.href}
                  target="_blank"
                  rel="noreferrer"
                  className="font-medium text-fg transition-colors hover:text-green"
                >
                  {backer.label}
                </a>
              </span>
            ))}
          </span>

          <a
            href={REPO}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1.5 text-label transition-colors hover:text-green"
          >
            <RiGithubFill size={15} />
            pivanov/ctx-wire
          </a>
        </div>
      </div>
    </footer>
  );
}
