import {
  RiGithubFill,
  RiLinkedinBoxFill,
  RiTwitterXFill,
} from "@remixicon/react";
import { IconCheck, IconCopy, IconStarFilled } from "@tabler/icons-react";
import { useCopy } from "../hooks/use-copy";
import { BrandMark } from "./brand-mark";

const REPO = "https://github.com/pivanov/ctx-wire";
const X_PROFILE = "https://x.com/ctxwire";
const INSTALL = "curl -fsSL https://ctx-wire.dev/install.sh | sh";

// Backer logos: mono marks render as a mask silhouette (background-color:
// currentColor clipped to the SVG) so they take the footer's muted color and
// hover to green. Color marks (sashido's blue+white robot, whose face is
// defined by color contrast, not shape) must render natively, a single-color
// mask would erase the face into a blob. w/h match each viewBox at 24px tall.
const BACKERS = [
  {
    label: "LogicStar AI",
    href: "https://logicstar.ai",
    src: "/logos/logicstar.svg",
    width: 138,
    height: 26,
    mono: true,
  },
  {
    label: "SashiDo.io",
    href: "https://www.sashido.io",
    src: "/logos/sashido.svg",
    width: 128,
    height: 32,
    mono: false,
  },
];

const SOCIALS = [
  { label: "GitHub", href: "https://github.com/pivanov/", Icon: RiGithubFill },
  { label: "X", href: "https://x.com/ivanovpavel", Icon: RiTwitterXFill },
  {
    label: "LinkedIn",
    href: "https://www.linkedin.com/in/ivanovpavel/",
    Icon: RiLinkedinBoxFill,
  },
];

export const Footer = () => {
  const [copied, copy] = useCopy();

  return (
    <footer className="relative z-10 mt-flow border-t border-line-soft bg-linear-to-b from-transparent to-panel/60">
      <div className="mx-auto w-full max-w-stage px-gutter">
        <div className="grid gap-10 py-12 lg:grid-cols-2 lg:items-center">
          <div>
            <div className="flex items-center gap-3.5">
              <img
                src="/pavel.jpg"
                alt="Pavel Ivanov"
                width={48}
                height={48}
                loading="lazy"
                className="size-12 rounded-full object-cover ring-1 ring-inset ring-line-soft"
              />
              <div className="leading-tight">
                <div className="font-mono text-base font-bold text-fg">
                  {"Hey, I'm Pavel"}
                </div>
                <div className="mt-0.5 font-mono text-2xs text-label">
                  ❤️ the web!
                </div>
              </div>
            </div>
            <p className="mt-4 max-w-copy font-mono text-cap leading-relaxed text-label">
              I built ctx-wire because I was tired of watching my AI agents burn
              tokens on noisy command output. It started as a weekend itch and
              kept going. Open source, MIT, and I am still tinkering, come say
              hi.
            </p>
            <div className="mt-5 flex items-center gap-2.5">
              {SOCIALS.map(({ Icon, href, label }) => (
                <a
                  key={label}
                  href={href}
                  target="_blank"
                  rel="noreferrer"
                  aria-label={label}
                  className="inline-flex size-8 items-center justify-center rounded-full text-label ring-1 ring-inset ring-line-soft transition-colors hover:text-green hover:ring-green/40"
                >
                  <Icon size={16} />
                </a>
              ))}
            </div>
          </div>

          <div className="flex flex-col gap-4 lg:items-end">
            <div className="flex items-center gap-2.5">
              <BrandMark size={30} />
              <span className="font-mono text-base font-bold tracking-tight text-fg">
                ctx-wire
              </span>
              <span className="rounded-full bg-green/10 px-2 py-0.5 font-mono text-2xs uppercase tracking-wider text-green ring-1 ring-inset ring-green/25">
                unleashed
              </span>
            </div>
            <p className="max-w-copy font-mono text-cap leading-relaxed text-label">
              A small Go binary that sits between AI coding agents and the noisy
              command output they pay tokens to read. Secrets-safe, with the
              full log kept on disk.
            </p>
            <span className="mt-1 font-mono text-2xs uppercase tracking-caps text-dim">
              Get started
            </span>
            <button
              type="button"
              onClick={() => copy(INSTALL)}
              title="Click to copy"
              className="no-scrollbar group inline-flex max-w-full items-center gap-3 overflow-x-auto rounded-card bg-screen px-4 py-2.5 font-mono text-2xs text-fg ring-1 ring-inset ring-line-soft transition-[box-shadow,transform] duration-150 ease-out hover:ring-green/40 motion-safe:active:scale-[0.98]"
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
            <div className="flex flex-wrap items-center gap-2">
              <a
                href={`${REPO}/stargazers`}
                target="_blank"
                rel="noreferrer"
                className="inline-flex items-center gap-1.5 rounded-full bg-green/10 px-3 py-1.5 font-mono text-2xs font-medium text-green ring-1 ring-inset ring-green/30 transition-colors hover:bg-green/20"
              >
                <IconStarFilled size={13} />
                Star on GitHub
              </a>
              <a
                href={X_PROFILE}
                target="_blank"
                rel="noreferrer"
                className="inline-flex items-center gap-1.5 rounded-full px-3 py-1.5 font-mono text-2xs font-medium text-fg ring-1 ring-inset ring-line-soft transition-colors hover:bg-green/10 hover:text-white"
              >
                <RiTwitterXFill size={13} />
                Follow @ctxwire
              </a>
            </div>
          </div>
        </div>

        <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-3 border-t border-line-soft py-6 font-mono text-2xs text-label">
          <span>© 2026 Pavel Ivanov · Released under MIT</span>

          <span className="inline-flex flex-wrap items-center gap-x-5 gap-y-2">
            <span className="uppercase tracking-caps text-label">
              Backed by
            </span>
            {BACKERS.map((backer) => (
              <a
                key={backer.label}
                href={backer.href}
                target="_blank"
                rel="noreferrer"
                className="inline-flex text-white opacity-80 transition duration-150 hover:opacity-100 motion-safe:active:scale-[0.98]"
              >
                <span className="sr-only">{backer.label}</span>
                {backer.mono ? (
                  <span
                    aria-hidden="true"
                    className="block bg-current"
                    style={{
                      width: backer.width,
                      height: backer.height,
                      maskImage: `url(${backer.src})`,
                      WebkitMaskImage: `url(${backer.src})`,
                      maskSize: "contain",
                      WebkitMaskSize: "contain",
                      maskRepeat: "no-repeat",
                      WebkitMaskRepeat: "no-repeat",
                      maskPosition: "center",
                      WebkitMaskPosition: "center",
                    }}
                  />
                ) : (
                  <img
                    src={backer.src}
                    alt=""
                    width={backer.width}
                    height={backer.height}
                    loading="lazy"
                    className="block"
                  />
                )}
              </a>
            ))}
          </span>
        </div>
      </div>
    </footer>
  );
};
