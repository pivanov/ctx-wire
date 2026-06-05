export type Totals = {
  installs?: number;
  commands?: number;
  raw_bytes?: number;
  emitted_bytes?: number;
  bytes_saved?: number;
  tokens_saved?: number;
  reports?: number;
};

export type ProgramStats = {
  program: string;
  runs?: number;
  count?: number;
  raw_bytes?: number;
  emitted_bytes?: number;
  bytes_saved?: number;
  tokens_saved?: number;
};

export type CountryStats = {
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

export type AgentStats = {
  agent: string;
  runs?: number;
  raw_bytes?: number;
  emitted_bytes?: number;
  bytes_saved?: number;
  tokens_saved?: number;
};

export type ImpactStats = {
  schema?: number;
  totals: Totals;
  programs: ProgramStats[];
  countries: CountryStats[];
  agents?: AgentStats[];
};

export type CountryMeta = {
  name: string;
  lat: number;
  lng: number;
};
