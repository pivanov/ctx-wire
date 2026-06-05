import { Footer } from "./components/footer";
import { GlobePanel } from "./components/globe-panel";
import { Hero } from "./components/hero";
import { HowItWorks } from "./components/how-it-works";
import { SavedByAgent } from "./components/saved-by-agent";
import { Stargazers } from "./components/stargazers";
import { TerminalWindow } from "./components/terminal-window";
import { TopBar } from "./components/top-bar";
import { useCommunity } from "./hooks/use-community";
import { useImpact } from "./hooks/use-impact";

export function App() {
  const { stats, version } = useImpact();
  const { stars, stargazers } = useCommunity();
  const totals = stats.totals || {};
  const live = Number(totals.commands || 0) > 0;

  return (
    <>
      <div className="aurora" aria-hidden="true" />
      <div className="grid-overlay" aria-hidden="true" />
      <div className="grain" aria-hidden="true" />

      <TopBar
        stars={stars}
        live={live}
        version={version}
        installs={Number(totals.installs || 0)}
        reports={Number(totals.reports || 0)}
      />

      <main className="relative z-10 flex w-full flex-col items-center gap-flow px-gutter pt-flow pb-15">
        <Hero stats={stats} />

        <HowItWorks />

        <section className="flex w-full max-w-term flex-col gap-4">
          <div className="flex items-baseline gap-4">
            <span className="relative pl-6 font-mono text-xs font-semibold uppercase tracking-kicker text-green before:absolute before:left-0 before:top-1/2 before:h-px before:w-3.5 before:bg-green">
              live impact
            </span>
            <p className="m-0 hidden font-mono text-xs text-label sm:block">
              Real telemetry, refreshed every few seconds.
            </p>
          </div>
          <TerminalWindow stats={stats} />
        </section>

        <div className="w-full max-w-stage">
          <GlobePanel stats={stats} />
        </div>

        <SavedByAgent stats={stats} />

        <Stargazers stargazers={stargazers} stars={stars} />
      </main>

      <Footer />
    </>
  );
}
