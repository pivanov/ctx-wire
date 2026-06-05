import { IconTerminal2 } from "@tabler/icons-react";

export function BrandMark({ size = 28 }: { size?: number }) {
  return (
    <span
      className="grid place-items-center rounded-lg bg-green/15 text-green ring-1 ring-inset ring-green/30"
      style={{ width: size, height: size }}
    >
      <IconTerminal2 size={Math.round(size * 0.56)} stroke={2.4} />
    </span>
  );
}
