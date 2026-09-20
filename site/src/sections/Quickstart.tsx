import { motion } from "motion/react";
import { Section } from "../components/Section";
import { reveal } from "../lib/reveal";

const STEPS: { prompt: string; command: string; comment?: string }[] = [
  {
    prompt: "$",
    command:
      "git clone https://github.com/dharmasaputraa/wiminder.git wiminder",
  },
  {
    prompt: "$",
    command: "cd wiminder && cp .env.example .env",
    comment: "# set APP_SECRET, CF_ACCESS_*, ADMIN_EMAILS",
  },
  { prompt: "$", command: "docker compose up -d --build" },
];

export function Quickstart() {
  return (
    <Section id="deploy">
      <div className="grid grid-cols-1 items-start gap-12 md:grid-cols-2">
        <motion.div {...reveal()}>
          <h2 className="text-[32px] font-[510] leading-[1.13] tracking-[-0.022em] text-paper md:text-[48px] md:leading-none">
            Running in three commands
          </h2>
          <p className="mt-4 text-[16px] leading-[1.5] text-fog">
            The SPA is embedded in the binary and SQLite lives on a volume.
            Public access goes through a Cloudflare tunnel with Cloudflare
            Access in front — no extra password to manage.
          </p>
          <a
            href="https://github.com/dharmasaputraa/wiminder#readme"
            className="mt-6 inline-block text-[15px] text-mist underline decoration-graphite underline-offset-4 transition-colors duration-150 hover:text-bone"
          >
            Read the full setup guide in the README
          </a>
        </motion.div>
        <motion.div
          {...reveal(0.1)}
          className="overflow-hidden rounded-xl bg-carbon shadow-card-inset"
        >
          <div className="flex items-center gap-1.5 border-b border-graphite px-6 py-4">
            <span className="size-2.5 rounded-full bg-graphite" />
            <span className="size-2.5 rounded-full bg-graphite" />
            <span className="size-2.5 rounded-full bg-graphite" />
          </div>
          <ol className="px-6 py-5 font-mono text-[13px] leading-[1.71] tracking-[-0.013em]">
            {STEPS.map(({ prompt, command, comment }) => (
              <li key={command}>
                <span className="text-fog">{prompt} </span>
                <span className="text-mist">{command}</span>
                {comment ? <span className="text-fog"> {comment}</span> : null}
              </li>
            ))}
          </ol>
        </motion.div>
      </div>
    </Section>
  );
}
