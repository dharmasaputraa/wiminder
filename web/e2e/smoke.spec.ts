import { realpath, stat } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { test, expect } from './fixtures'
import { rows } from './helpers/db'

test('healthz responds ok', async ({ app }) => {
  const res = await fetch(`${app.baseUrl}/healthz`)
  expect(res.status).toBe(200)
  await expect(res.json()).resolves.toEqual({ ok: true })
})

test('serves the embedded SPA on / and on deep links', async ({ app }) => {
  for (const p of ['/', '/reminder/contacts']) {
    const res = await fetch(`${app.baseUrl}${p}`)
    expect(res.status).toBe(200)
    const html = await res.text()
    expect(html).toContain('<div id="root"')
  }
})

test('migrations created the full schema', async ({ app }) => {
  const tables = rows(app.db, `SELECT name FROM sqlite_master WHERE type = 'table'`).map((r) => r.name)
  for (const t of [
    'users', 'contacts', 'occasions', 'reminder_prefs', 'occasion_prefs',
    'channels', 'notification_log', 'settings', 'holiday_cache', 'schema_migrations',
  ]) {
    expect(tables, `table ${t}`).toContain(t)
  }
})

test('app runs against a temp database under os.tmpdir()', async ({ app }) => {
  // Worker-local check only: a worker may run several spec files over one
  // lifetime, so no assertion may depend on the DB still being pristine.
  const dataDir = path.dirname(app.dbPath)
  const tmpDir = await realpath(os.tmpdir())
  const realDataDir = await realpath(dataDir)
  expect(realDataDir.startsWith(tmpDir + path.sep), `${realDataDir} is under ${tmpDir}`).toBe(true)
  expect(path.basename(dataDir)).toMatch(/^wiminder-e2e-/)
  const s = await stat(app.dbPath)
  expect(s.size).toBeGreaterThan(0)
  expect(rows(app.db, 'SELECT 1 AS one')[0].one).toBe(1)
})
