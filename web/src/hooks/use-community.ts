import { useEffect, useState } from "react";
import { STARGAZER_REPO } from "../data";

export type Stargazer = {
  login: string;
  avatar: string;
  url: string;
};

export type Community = {
  stars: number;
  stargazers: Stargazer[];
};

type GhUser = { login: string; avatar_url: string; html_url: string };

export function useCommunity(): Community {
  const [data, setData] = useState<Community>({ stars: 0, stargazers: [] });

  useEffect(() => {
    let cancelled = false;

    async function viaGitHub(): Promise<Community | null> {
      try {
        const base = `https://api.github.com/repos/${STARGAZER_REPO}`;
        const [repoRes, starRes] = await Promise.all([
          fetch(base, { cache: "no-store" }),
          fetch(`${base}/stargazers?per_page=100`, { cache: "no-store" }),
        ]);
        const repo = repoRes.ok
          ? ((await repoRes.json()) as { stargazers_count?: number })
          : {};
        const list = starRes.ok ? ((await starRes.json()) as GhUser[]) : [];
        const stargazers = Array.isArray(list)
          ? list.map((u) => ({
              login: u.login,
              avatar: u.avatar_url,
              url: u.html_url,
            }))
          : [];
        return {
          stars: Number(repo.stargazers_count || stargazers.length || 0),
          stargazers,
        };
      } catch {
        return null;
      }
    }

    async function load() {
      const result = await viaGitHub();
      if (result && !cancelled) setData(result);
    }

    load();
    return () => {
      cancelled = true;
    };
  }, []);

  return data;
}
