export function ScreenshotFrame({
  src,
  label,
  loading = "lazy",
}: {
  src: string;
  label: string;
  loading?: "eager" | "lazy";
}) {
  return (
    <div className="rounded-xl bg-carbon p-6 shadow-card-inset">
      <div className="flex items-center gap-1.5 pb-4">
        <span className="size-2.5 rounded-full bg-graphite" />
        <span className="size-2.5 rounded-full bg-graphite" />
        <span className="size-2.5 rounded-full bg-graphite" />
      </div>
      <img
        src={src}
        alt={label}
        loading={loading}
        decoding="async"
        className="w-full rounded-md outline-1 outline-white/10 -outline-offset-1"
      />
    </div>
  );
}
