/** Shared scroll-reveal props: subtle 12px rise + fade, once, honoring
 *  reduced motion via the MotionConfig in App. Stagger chunks ~100ms. */
export function reveal(delay = 0) {
  return {
    initial: { opacity: 0, y: 12 },
    whileInView: { opacity: 1, y: 0 },
    viewport: { once: true, margin: "0px 0px -64px 0px" },
    transition: { duration: 0.4, delay, ease: "easeOut" as const },
  };
}
