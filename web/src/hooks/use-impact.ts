import { useEffect, useState } from "react";
import { emptyStats, IMPACT_ENDPOINT, POLL_MS } from "../data";
import type { TImpactStats } from "../types";

type TImpactState = {
  stats: TImpactStats;
  version: number;
};

export const useImpact = (): TImpactState => {
  const [state, setState] = useState<TImpactState>({
    stats: emptyStats,
    version: 0,
  });

  useEffect(() => {
    let cancelled = false;

    const load = async () => {
      try {
        const response = await fetch(IMPACT_ENDPOINT, { cache: "no-store" });
        if (!response.ok) {
          return;
        }
        const next = (await response.json()) as Partial<TImpactStats>;
        if (!next.totals || Number(next.totals.commands || 0) === 0) {
          return;
        }
        if (cancelled) {
          return;
        }
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
    };

    let timer: number | undefined;

    const start = () => {
      if (timer !== undefined) {
        return;
      }
      load();
      timer = window.setInterval(load, POLL_MS);
    };

    const stop = () => {
      if (timer === undefined) {
        return;
      }
      window.clearInterval(timer);
      timer = undefined;
    };

    // Pause polling while the tab is hidden; resume with an immediate refresh
    // when it becomes active again.
    const onVisibility = () => {
      if (document.hidden) {
        stop();
      } else {
        start();
      }
    };

    if (!document.hidden) {
      start();
    }
    document.addEventListener("visibilitychange", onVisibility);

    return () => {
      cancelled = true;
      stop();
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, []);

  return state;
};
