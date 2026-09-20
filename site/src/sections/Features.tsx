import { motion } from "motion/react";
import {
  Bell,
  CalendarSync,
  Fingerprint,
  History,
  MoonStar,
  Smartphone,
} from "lucide-react";
import { Section } from "../components/Section";
import { reveal } from "../lib/reveal";

const FEATURES = [
  {
    icon: MoonStar,
    title: "Otonan & pawukon",
    body: "The 210-day cycle computed from each birth date, with saptawara, pancawara, and wuku labels — no calendar watching.",
  },
  {
    icon: Bell,
    title: "Every channel you run",
    body: "Gotify, Telegram, or email. Channel configs are encrypted with AES-256-GCM and testable with one click.",
  },
  {
    icon: History,
    title: "Never miss a reminder",
    body: "Reminders missed while the container was down are caught up and labeled late; deduplication guarantees once per event, date, and offset.",
  },
  {
    icon: Fingerprint,
    title: "One container",
    body: "A single Go binary with the web UI embedded and SQLite on a volume. No external services required.",
  },
  {
    icon: CalendarSync,
    title: "More than otonan",
    body: "Birthdays (yes, Feb 29), anniversaries, and Pawukon holidays like Galungan, Kuningan, Saraswati, and Pagerwesi.",
  },
  {
    icon: Smartphone,
    title: "Installable PWA",
    body: "Add it to your phone's home screen; the calendar navigates across years with data fetched per range.",
  },
] as const;

export function Features() {
  return (
    <Section id="features">
      <motion.div {...reveal()}>
        <h2 className="text-[32px] font-[510] leading-[1.13] tracking-[-0.022em] text-paper md:text-[48px] md:leading-none">
          Quietly keeps track
        </h2>
        <p className="mt-4 max-w-[560px] text-[16px] leading-[1.5] text-fog">
          Everything wiminder does, it does on your own infrastructure.
        </p>
      </motion.div>
      <div className="mt-12 grid grid-cols-1 gap-4 md:grid-cols-2">
        {FEATURES.map(({ icon: Icon, title, body }, i) => (
          <motion.article
            key={title}
            {...reveal(0.1 + i * 0.08)}
            className="group rounded-xl bg-[rgba(255,255,255,0.02)] p-6 shadow-card-inset transition-colors duration-150 hover:bg-[rgba(255,255,255,0.04)]"
          >
            <Icon
              className="size-4 text-fog transition-colors duration-150 group-hover:text-mist"
              aria-hidden
            />
            <h3 className="mt-4 text-[20px] font-[510] leading-[1.33] tracking-[-0.012em] text-mist">
              {title}
            </h3>
            <p className="mt-2 text-[15px] leading-[1.6] tracking-[-0.011em] text-fog">
              {body}
            </p>
          </motion.article>
        ))}
      </div>
    </Section>
  );
}
