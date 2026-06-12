import { useEffect, useState } from "react";
import { COMMUNITY_ENDPOINT } from "../data";

export type TStargazer = {
  login: string;
  avatar: string;
  url: string;
};

export type TCommunity = {
  stars: number;
  stargazers: TStargazer[];
};

export const useCommunity = (): TCommunity => {
  const [data, setData] = useState<TCommunity>({ stars: 0, stargazers: [] });

  useEffect(() => {
    let cancelled = false;

    // The browser only ever talks to the telemetry worker; it proxies + caches
    // GitHub server-side (1h fresh, 7d stale on 403), so visitors never hit
    // GitHub's 60/hr unauthenticated limit and the count never collapses to 0.
    const load = async () => {
      try {
        const res = await fetch(COMMUNITY_ENDPOINT, { cache: "no-store" });
        if (!res.ok) {
          return;
        }
        const json = (await res.json()) as Partial<TCommunity>;
        if (cancelled) {
          return;
        }
        setData({
          stars: Number(json.stars || 0),
          stargazers: Array.isArray(json.stargazers) ? json.stargazers : [],
        });
      } catch {
        // worker unreachable: keep the empty state rather than calling GitHub
      }
    };

    load();
    return () => {
      cancelled = true;
    };
  }, []);

  return data;
};
