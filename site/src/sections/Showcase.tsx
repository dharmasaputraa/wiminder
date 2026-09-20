import { motion } from "motion/react";
import { ScreenshotFrame } from "../components/ScreenshotFrame";
import { reveal } from "../lib/reveal";
import contactDetailShot from "../assets/shots/contact-detail.png";
import contactsShot from "../assets/shots/contacts.png";

export function Showcase() {
  return (
    <section className="mx-auto w-full max-w-[1200px] px-6 py-[96px]">
      <p className="mb-8 text-center text-caption text-fog">
        Your whole family's calendar — every otonan, birthday, and anniversary
        in one place
      </p>
      <div className="mx-auto grid max-w-[1200px] grid-cols-1 gap-4 md:grid-cols-2">
        <motion.div {...reveal()}>
          <ScreenshotFrame
            src={contactsShot}
            label="Contacts with countdown badges and occasion chips"
          />
        </motion.div>
        <motion.div {...reveal(0.1)}>
          <ScreenshotFrame
            src={contactDetailShot}
            label="Contact detail — otonan and birthday occasions with pawukon labels"
          />
        </motion.div>
      </div>
    </section>
  );
}
