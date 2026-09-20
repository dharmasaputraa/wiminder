import type { AnchorHTMLAttributes } from "react";
import { cx } from "../lib/cx";

type Variant = "primary" | "ghost" | "white-pill";

const styles: Record<Variant, string> = {
  // The ONLY chromatic button — hero CTA (Global Constraints).
  primary:
    "bg-acid-lime text-void rounded-md px-4 py-2.5 text-[14px] font-[510] tracking-[-0.011em] hover:opacity-90",
  ghost:
    "border border-graphite text-mist rounded-md px-3 py-2 text-[13px] hover:border-smoke hover:text-bone",
  "white-pill":
    "bg-paper text-void rounded-full px-4 py-2 text-[13px] font-[510] hover:opacity-90",
};

export function Button({
  variant = "ghost",
  className,
  ...props
}: { variant?: Variant } & AnchorHTMLAttributes<HTMLAnchorElement>) {
  return (
    <a
      className={cx(
        "inline-flex items-center gap-2 transition-[color,border-color,opacity,transform] duration-150 ease-out active:scale-[0.96]",
        styles[variant],
        className,
      )}
      {...props}
    />
  );
}
