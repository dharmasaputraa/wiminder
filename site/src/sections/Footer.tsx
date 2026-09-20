const REPO = "https://github.com/dharmasaputraa/wiminder";

export function Footer() {
  return (
    <footer className="border-t border-graphite">
      <div className="mx-auto flex w-full max-w-[1200px] flex-col gap-3 px-6 py-12 text-caption text-fog md:flex-row md:items-center md:justify-between">
        <p>
          Built for the Balinese community.{" "}
          <a
            href={REPO}
            className="text-fog underline decoration-graphite underline-offset-4 transition-colors duration-150 hover:text-mist"
          >
            View source on GitHub
          </a>
        </p>
        <p>
          Pawukon fixtures ©{" "}
          <a
            href="https://kalenderbali.org"
            className="text-fog underline decoration-graphite underline-offset-4 transition-colors duration-150 hover:text-mist"
          >
            kalenderbali.org
          </a>{" "}
          (I Wayan Nuarsa, Universitas Udayana) — personal test fixtures,
          not redistributed.
        </p>
      </div>
    </footer>
  );
}
