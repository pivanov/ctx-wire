import { CommandCuts } from "./components/command-cuts";
import { ComparisonRtk } from "./components/comparison-rtk";
import { ErrorBoundary } from "./components/error-boundary";
import { Faq } from "./components/faq";
import { Footer } from "./components/footer";
import { GlobePanel } from "./components/globe-panel";
import { Hero } from "./components/hero";
import { HowItWorks } from "./components/how-it-works";
import { SavedByAgent } from "./components/saved-by-agent";
import { SectionKicker } from "./components/section-heading";
import { Stargazers } from "./components/stargazers";
import { TerminalWindow } from "./components/terminal-window";
import { TopBar } from "./components/top-bar";
import { useCommunity } from "./hooks/use-community";
import { useImpact } from "./hooks/use-impact";

export const App = () => {
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

        <ErrorBoundary>
          <CommandCuts stats={stats} />
        </ErrorBoundary>

        <section className="flex w-full max-w-term flex-col gap-4">
          <SectionKicker desc="Real telemetry, refreshed every few seconds.">
            live impact
          </SectionKicker>
          <TerminalWindow stats={stats} />
        </section>

        <ErrorBoundary>
          <div className="w-full max-w-stage">
            <GlobePanel stats={stats} />
          </div>
        </ErrorBoundary>

        <ErrorBoundary>
          <SavedByAgent stats={stats} />
        </ErrorBoundary>

        <ComparisonRtk />

        <Stargazers stargazers={stargazers} stars={stars} />

        <Faq />
      </main>

      <Footer />
    </>
  );
};
