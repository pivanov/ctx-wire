import { useState } from "react";

// Every supported agent maps to a custom brand SVG in web/public/logos/<id>.svg.
// Drop a file in and it shows automatically; until then the tile falls back to
// a monogram. Keep the id in sync with the AGENTS list in hero.tsx.
type TAgent = { label: string; src: string; size?: number };

const AGENTS: Record<string, TAgent> = {
  claude: { label: "Claude", src: "/logos/claude.svg", size: 24 },
  codex: { label: "Codex", src: "/logos/codex.svg", size: 32 },
  cursor: { label: "Cursor", src: "/logos/cursor.svg", size: 24 },
  gemini: { label: "Gemini", src: "/logos/gemini.svg", size: 24 },
  copilot: { label: "Copilot", src: "/logos/copilot.svg", size: 24 },
  cline: { label: "Cline", src: "/logos/cline.svg", size: 24 },
  windsurf: { label: "Windsurf", src: "/logos/windsurf.svg", size: 24 },
  antigravity: {
    label: "Antigravity",
    src: "/logos/antigravity.svg",
    size: 24,
  },
  hermes: { label: "Hermes", src: "/logos/hermes.svg", size: 24 },
  kilocode: { label: "Kilo Code", src: "/logos/kilocode.svg", size: 24 },
  opencode: { label: "OpenCode", src: "/logos/opencode.svg", size: 24 },
  pi: { label: "Pi", src: "/logos/pi.svg", size: 24 },
  visualstudio: {
    label: "Visual Studio",
    src: "/logos/visualstudio.svg",
    size: 24,
  },
  vscode: { label: "VS Code", src: "/logos/vscode.svg", size: 24 },
};

export const agentLabel = (name: string): string => {
  return AGENTS[name]?.label ?? name.charAt(0).toUpperCase() + name.slice(1);
};

export const AgentLogo = ({
  name,
  size = 24,
}: {
  name: string;
  size?: number;
}) => {
  const [failed, setFailed] = useState(false);
  const src = AGENTS[name]?.src;
  const iconSize = AGENTS[name]?.size || size;

  if (src && !failed) {
    return (
      <img
        src={src}
        alt={agentLabel(name)}
        width={iconSize}
        height={iconSize}
        loading="lazy"
        onError={() => setFailed(true)}
        className="object-contain"
      />
    );
  }

  return (
    <span aria-hidden="true" className="font-mono text-sm font-bold text-ink">
      {agentLabel(name).charAt(0)}
    </span>
  );
};
