// Generates the landing page screenshots (site/src/assets/shots/) by running a
// real dev-mode server with seeded demo data. Not part of the e2e suite.
//
// Usage:  cd web && node e2e/site-screenshots.mjs
// Needs:  make build first (bin/wiminder with the embedded SPA).

import { chromium } from '@playwright/test'
import { spawn } from 'node:child_process'
import { createWriteStream } from 'node:fs'
import { mkdir, mkdtemp, rm } from 'node:fs/promises'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const APP_SECRET = 'site-shots-secret-0123456789'
const ADMIN = 'admin@local.test'
const OUT_DIR = fileURLToPath(new URL('../../site/src/assets/shots/', import.meta.url))

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

function freePort() {
  return new Promise((resolve, reject) => {
    const srv = net.createServer()
    srv.listen(0, '127.0.0.1', () => {
      const { port } = srv.address()
      srv.close(() => resolve(port))
    })
    srv.on('error', reject)
  })
}

async function startApp() {
  const dataDir = await mkdtemp(path.join(os.tmpdir(), 'wiminder-shots-'))
  const logPath = path.join(dataDir, 'server.log')
  const port = await freePort()
  const baseUrl = `http://127.0.0.1:${port}`
  const bin = fileURLToPath(new URL('../../bin/wiminder', import.meta.url))
  const logStream = createWriteStream(logPath)
  const child = spawn(bin, [], {
    env: {
      ...process.env,
      ADDR: `127.0.0.1:${port}`,
      DATA_DIR: dataDir,
      AUTH_MODE: 'dev',
      APP_SECRET,
      ADMIN_EMAILS: ADMIN,
      TZ: 'Asia/Makassar',
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  child.stdout.pipe(logStream)
  child.stderr.pipe(logStream)

  let up = false
  for (let i = 0; i < 150 && !up; i++) {
    if (child.exitCode !== null) throw new Error(`server exited — see ${logPath}`)
    try {
      const res = await fetch(`${baseUrl}/healthz`, { signal: AbortSignal.timeout(2_000) })
      up = res.ok
    } catch {
      /* not listening yet */
    }
    if (!up) await sleep(200)
  }
  if (!up) throw new Error(`server did not become healthy — see ${logPath}`)

  const stop = async () => {
    if (child.exitCode === null) child.kill('SIGKILL')
    logStream.end()
    await rm(dataDir, { recursive: true, force: true })
  }
  return { baseUrl, stop }
}

const api = async (baseUrl, path_, data) => {
  const res = await fetch(`${baseUrl}/api/v1${path_}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Dev-Email': ADMIN },
    body: JSON.stringify(data),
  })
  if (res.status !== 201) throw new Error(`seed ${path_} -> ${res.status}: ${await res.text()}`)
  return res.json()
}

async function seed(baseUrl) {
  // Dates are spread so the current window always shows otonan + birthday + anniversary badges.
  const people = [
    { name: 'Wayan Sudira', nickname: 'Pak Wayan', occasions: [
      { type: 'otonan', date: '1962-04-17', recurrence: 'otonan' },
      { type: 'birthday', date: '1962-04-17', recurrence: 'yearly' },
    ] },
    { name: 'Ni Luh Ayu Widiarti', nickname: 'Ayu', occasions: [
      { type: 'birthday', date: '1990-11-02', recurrence: 'yearly' },
    ] },
    { name: 'Made Wirawan', nickname: 'Pak Made', occasions: [
      { type: 'otonan', date: '1978-08-09', recurrence: 'otonan' },
      { type: 'anniversary', date: '2010-10-10', recurrence: 'yearly', label: 'Wedding anniversary' },
    ] },
    { name: 'Kadek Rahayu', nickname: 'Dek Kadek', occasions: [
      { type: 'birthday', date: '2016-06-21', recurrence: 'yearly' },
    ] },
    { name: 'Gede Sanjaya', occasions: [
      { type: 'otonan', date: '1985-02-14', recurrence: 'otonan' },
    ] },
    { name: 'Ni Made Sari', nickname: 'Bu Sari', occasions: [
      { type: 'birthday', date: '1958-09-30', recurrence: 'yearly' },
    ] },
  ]
  const ids = []
  for (const p of people) {
    const { id } = await api(baseUrl, '/contacts', { name: p.name, nickname: p.nickname })
    ids.push(id)
    for (const occ of p.occasions) await api(baseUrl, `/contacts/${id}/occasions`, occ)
  }
  // One channel so the calendar page does not show its "no channels" banner.
  await api(baseUrl, '/channels', {
    type: 'gotify',
    name: 'Family reminders',
    config: { base_url: 'http://127.0.0.1:9', token: 'AzeGq9_FxgeFMkz', priority: 7 },
  })
  return ids
}

async function main() {
  await mkdir(OUT_DIR, { recursive: true })
  const { baseUrl, stop } = await startApp()
  let browser
  try {
    const ids = await seed(baseUrl)
    browser = await chromium.launch()
    const ctx = await browser.newContext({
      baseURL: baseUrl,
      viewport: { width: 1440, height: 900 },
      deviceScaleFactor: 2,
      colorScheme: 'dark',
      extraHTTPHeaders: { 'X-Dev-Email': ADMIN },
    })
    await ctx.addInitScript(() => {
      localStorage.setItem('wiminder-dev-email', 'admin@local.test')
      localStorage.setItem('wiminder.theme', 'dark')
    })
    const page = await ctx.newPage()

    const shot = async (url, file, settle = 1400) => {
      await page.goto(url, { waitUntil: 'networkidle' })
      await sleep(settle)
      await page.screenshot({ path: path.join(OUT_DIR, file) })
      console.log(`captured ${file}`)
    }

    // Element shot of the exact calendar card (toolbar + month grid + badges).
    await page.goto('/reminder', { waitUntil: 'networkidle' })
    await sleep(1600)
    await page
      .locator('div.min-w-0.flex-1.overflow-hidden.rounded-xl.border.bg-card')
      .first()
      .screenshot({ path: path.join(OUT_DIR, 'calendar-hero.png') })
    console.log('captured calendar-hero.png')

    await shot('/reminder', 'calendar.png')
    await shot('/reminder/contacts', 'contacts.png')
    await shot(`/reminder/contacts/${ids[0]}`, 'contact-detail.png')
    await ctx.close()
  } finally {
    await browser?.close().catch(() => {})
    await stop()
  }
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
