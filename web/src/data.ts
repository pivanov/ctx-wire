import type { CountryMeta, ImpactStats } from "./types";

export const IMPACT_ENDPOINT =
  "https://ctx-wire-telemetry.iweb-ivanov.workers.dev/v1/impact";

export const POLL_MS = 5000;

// USD per 1M tokens for the site-side savings estimate (the CLI sends tokens,
// never dollars). Swappable; always shown as a labeled estimate.
export const TOKEN_PRICE_PER_M = 3;

// Public repo; stars + stargazers come from the GitHub API.
export const STARGAZER_REPO = "pivanov/ctx-wire";

export const emptyStats: ImpactStats = {
  totals: {},
  programs: [],
  countries: [],
};

export const countryMeta: Record<string, CountryMeta> = {
  AR: { name: "Argentina", lat: -38.42, lng: -63.62 },
  AU: { name: "Australia", lat: -25.27, lng: 133.78 },
  BE: { name: "Belgium", lat: 50.5, lng: 4.47 },
  BG: { name: "Bulgaria", lat: 42.73, lng: 25.49 },
  BR: { name: "Brazil", lat: -14.24, lng: -51.93 },
  CA: { name: "Canada", lat: 56.13, lng: -106.35 },
  CH: { name: "Switzerland", lat: 46.82, lng: 8.23 },
  CN: { name: "China", lat: 35.86, lng: 104.2 },
  CZ: { name: "Czechia", lat: 49.82, lng: 15.47 },
  DE: { name: "Germany", lat: 51.17, lng: 10.45 },
  DK: { name: "Denmark", lat: 56.26, lng: 9.5 },
  ES: { name: "Spain", lat: 40.46, lng: -3.75 },
  FI: { name: "Finland", lat: 61.92, lng: 25.75 },
  FR: { name: "France", lat: 46.23, lng: 2.21 },
  GB: { name: "United Kingdom", lat: 55.38, lng: -3.44 },
  GR: { name: "Greece", lat: 39.07, lng: 21.82 },
  ID: { name: "Indonesia", lat: -0.79, lng: 113.92 },
  IE: { name: "Ireland", lat: 53.41, lng: -8.24 },
  IL: { name: "Israel", lat: 31.05, lng: 34.85 },
  IN: { name: "India", lat: 20.59, lng: 78.96 },
  IT: { name: "Italy", lat: 41.87, lng: 12.57 },
  JP: { name: "Japan", lat: 36.2, lng: 138.25 },
  KR: { name: "South Korea", lat: 35.91, lng: 127.77 },
  MX: { name: "Mexico", lat: 23.63, lng: -102.55 },
  NL: { name: "Netherlands", lat: 52.13, lng: 5.29 },
  NO: { name: "Norway", lat: 60.47, lng: 8.47 },
  PL: { name: "Poland", lat: 51.92, lng: 19.15 },
  PT: { name: "Portugal", lat: 39.4, lng: -8.22 },
  RO: { name: "Romania", lat: 45.94, lng: 24.97 },
  RS: { name: "Serbia", lat: 44.02, lng: 21.01 },
  RU: { name: "Russia", lat: 61.52, lng: 105.32 },
  SE: { name: "Sweden", lat: 60.13, lng: 18.64 },
  SG: { name: "Singapore", lat: 1.35, lng: 103.82 },
  TR: { name: "Turkey", lat: 38.96, lng: 35.24 },
  UA: { name: "Ukraine", lat: 48.38, lng: 31.17 },
  US: { name: "United States", lat: 37.09, lng: -95.71 },
  ZA: { name: "South Africa", lat: -30.56, lng: 22.94 },
};
