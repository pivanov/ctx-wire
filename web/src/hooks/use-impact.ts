import { useEffect, useState } from "react";
import { emptyStats, IMPACT_ENDPOINT, POLL_MS } from "../data";
import type { ImpactStats } from "../types";

type ImpactState = {
  stats: ImpactStats;
  version: number;
};

export function useImpact(): ImpactState {
  const [state, setState] = useState<ImpactState>({
    stats: emptyStats,
    version: 0,
  });

  useEffect(() => {
    let cancelled = false;

    async function load() {
      try {
        const response = await fetch(IMPACT_ENDPOINT, { cache: "no-store" });
        if (!response.ok) return;
        const next = (await response.json()) as Partial<ImpactStats>;
        if (!next.totals || Number(next.totals.commands || 0) === 0) return;
        if (cancelled) return;
        setState((prev) => {
          const changed =
            Number(next.totals?.commands || 0) !==
            Number(prev.stats.totals?.commands || 0);
          return {
            stats: {
              totals: next.totals || {},
              programs: next.programs || [],
              countries: next.countries || [],
              agents: next.agents || [],
            },
            version: changed ? prev.version + 1 : prev.version,
          };
        });
      } catch {
        // Keep terminal numbers real-only. The globe has its own visual demo data.
      }
    }

    let timer: number | undefined;

    function start() {
      if (timer !== undefined) return;
      load();
      timer = window.setInterval(load, POLL_MS);
    }

    function stop() {
      if (timer === undefined) return;
      window.clearInterval(timer);
      timer = undefined;
    }

    function onVisibility() {
      // Pause polling while the tab is hidden; resume with an immediate refresh
      // when it becomes active again.
      if (document.hidden) stop();
      else start();
    }

    if (!document.hidden) start();
    document.addEventListener("visibilitychange", onVisibility);

    return () => {
      cancelled = true;
      stop();
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, []);

  return state;
}
