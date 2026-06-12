import { useCallback, useRef, useState } from "react";

export const useCopy = (): [boolean, (text: string) => void] => {
  const [copied, setCopied] = useState(false);
  const refTimer = useRef<number | null>(null);

  const copy = useCallback((text: string) => {
    void navigator.clipboard?.writeText(text).then(() => {
      setCopied(true);
      if (refTimer.current !== null) {
        window.clearTimeout(refTimer.current);
      }
      refTimer.current = window.setTimeout(() => setCopied(false), 1400);
    });
  }, []);

  return [copied, copy];
};
