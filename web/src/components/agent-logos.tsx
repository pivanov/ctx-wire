import {
  siClaude,
  siCline,
  siCursor,
  siGithubcopilot,
  siGooglegemini,
  siWindsurf,
} from "simple-icons";

type Brand = { title: string; path: string; hex: string };

// Telemetry normalizes the agent name to lowercase. Codex has no OpenAI mark in
// simple-icons (it was removed), so it falls back to a monogram.
const AGENTS: Record<string, { label: string; brand?: Brand }> = {
  claude: { label: "Claude", brand: siClaude },
  codex: { label: "Codex" },
  cursor: { label: "Cursor", brand: siCursor },
  gemini: { label: "Gemini", brand: siGooglegemini },
  copilot: { label: "Copilot", brand: siGithubcopilot },
  cline: { label: "Cline", brand: siCline },
  windsurf: { label: "Windsurf", brand: siWindsurf },
};

export function agentLabel(name: string): string {
  return AGENTS[name]?.label ?? name.charAt(0).toUpperCase() + name.slice(1);
}

export function AgentLogo({
  name,
  size = 18,
}: {
  name: string;
  size?: number;
}) {
  const brand = AGENTS[name]?.brand;
  if (brand) {
    return (
      <svg
        aria-hidden="true"
        viewBox="0 0 24 24"
        width={size}
        height={size}
        fill={`#${brand.hex}`}
      >
        <path d={brand.path} />
      </svg>
    );
  }
  return (
    <span aria-hidden="true" className="font-mono text-sm font-bold text-ink">
      {agentLabel(name).charAt(0)}
    </span>
  );
}
