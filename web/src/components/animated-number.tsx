import {
  animate,
  motion,
  useMotionValue,
  useReducedMotion,
  useTransform,
} from "motion/react";
import { useEffect, useState } from "react";

// Counts up to `value` when it first scrolls into view. The formatted text is a
// derived motion value rendered as a child, so motion updates the DOM directly
// each frame, no per-frame React re-render. Reduced-motion snaps to the value.
export const AnimatedNumber = ({
  className,
  format,
  value,
}: {
  className?: string;
  format: (n: number) => string;
  value: number;
}) => {
  const reduce = useReducedMotion();
  const mv = useMotionValue(reduce ? value : 0);
  const text = useTransform(mv, format);
  const [inView, setInView] = useState(false);

  useEffect(() => {
    if (!inView) {
      return;
    }
    if (reduce) {
      mv.set(value);
      return;
    }
    const controls = animate(mv, value, {
      duration: 1.4,
      ease: [0.16, 1, 0.3, 1],
    });
    return () => controls.stop();
  }, [inView, value, reduce, mv]);

  return (
    <motion.span
      className={className}
      onViewportEnter={() => setInView(true)}
      viewport={{ once: true, amount: 0.6 }}
    >
      {text}
    </motion.span>
  );
};
