export type TTotals = {
  installs?: number;
  commands?: number;
  raw_bytes?: number;
  emitted_bytes?: number;
  bytes_saved?: number;
  tokens_saved?: number;
  reports?: number;
};

export type TProgramStats = {
  program: string;
  runs?: number;
  count?: number;
  raw_bytes?: number;
  emitted_bytes?: number;
  bytes_saved?: number;
  tokens_saved?: number;
};

export type TCountryStats = {
  country?: string;
  country_code?: string;
  code?: string;
  installs?: number;
  commands?: number;
  raw_bytes?: number;
  emitted_bytes?: number;
  bytes_saved?: number;
  tokens_saved?: number;
  reports?: number;
};

export type TAgentStats = {
  agent: string;
  runs?: number;
  raw_bytes?: number;
  emitted_bytes?: number;
  bytes_saved?: number;
  tokens_saved?: number;
};

export type TImpactStats = {
  schema?: number;
  totals: TTotals;
  programs: TProgramStats[];
  countries: TCountryStats[];
  agents?: TAgentStats[];
};

export type TCountryMeta = {
  name: string;
  lat: number;
  lng: number;
};
