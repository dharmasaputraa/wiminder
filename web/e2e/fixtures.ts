import { test as base, expect } from '@playwright/test'
import { spawn } from 'node:child_process'
import { createWriteStream } from 'node:fs'
import { mkdtemp, rm } from 'node:fs/promises'
import { createServer, type Server } from 'node:http'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { DatabaseSync } from 'node:sqlite'
import type { APIRequestContext, BrowserContext, BrowserContextOptions, Page } from '@playwright/test'

export const APP_SECRET = 'e2e-app-secret-0123456789'
export const ADMIN = 'admin@local.test'
export const MEMBER_A = 'member-a@local.test'
export const MEMBER_B = 'member-b@local.test'

export type StubMessage = { title: string; message: string; priority: number; token: string }

export type App = {
  baseUrl: string
  dataDir: string
  dbPath: string
  db: DatabaseSync
  logPath: string
  stubUrl: string
  stubMessages: () => StubMessage[]
  stubServer: Server
}

function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer()
    srv.listen(0, '127.0.0.1', () => {
      const { port } = srv.address() as net.AddressInfo
      srv.close(() => resolve(port))
    })
    srv.on('error', reject)
  })
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

function startGotifyStub(): Promise<{ server: Server; url: string; messages: () => StubMessage[] }> {
  return new Promise((resolve) => {
    const received: StubMessage[] = []
    const server = createServer((req, res) => {
      const chunks: Buffer[] = []
      req.on('data', (c: Buffer) => chunks.push(c))
      req.on('end', () => {
        if (req.method === 'POST') {
          try {
            const body = JSON.parse(Buffer.concat(chunks).toString('utf8'))
            const token = new URL(req.url ?? '', 'http://x').searchParams.get('token') ?? ''
            received.push({ title: body.title, message: body.message, priority: body.priority, token })
          } catch { /* not JSON — ignore */ }
        }
        res.writeHead(200, { 'Content-Type': 'application/json' })
        res.end('{"id":1}')
      })
    })
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address() as net.AddressInfo
      resolve({ server, url: `http://127.0.0.1:${addr.port}`, messages: () => [...received] })
    })
  })
}

async function startApp(): Promise<{ app: App; stop: () => Promise<void> }> {
  const workerIndex = process.env.TEST_PARALLEL_INDEX ?? '0'
  const dataDir = await mkdtemp(path.join(os.tmpdir(), `wiminder-e2e-${process.pid}-${workerIndex}-`))
  const dbPath = path.join(dataDir, 'wimember.db')
  const logPath = path.join(dataDir, 'server.log')
  const port = await freePort()
  const baseUrl = `http://127.0.0.1:${port}`
  const bin = fileURLToPath(new URL('../../bin/wiminder', import.meta.url)) // ESM-safe: no __dirname under type:module

  const logStream = createWriteStream(logPath)
  const child = spawn(bin, [], {
    env: {
      ...process.env,
      ADDR: `127.0.0.1:${port}`,
      DATA_DIR: dataDir,
      AUTH_MODE: 'dev',
      APP_SECRET,
      ADMIN_EMAILS: ADMIN,
      TZ: 'UTC',
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  child.stdout.pipe(logStream)
  child.stderr.pipe(logStream)

  // Fail fast (instead of polling for 30s) when the process dies or cannot spawn.
  let spawnError: Error | undefined
  child.on('error', (err) => {
    spawnError = err
  })
  child.on('exit', (code) => {
    if (code !== null && code !== 0) {
      console.error(`[e2e] server exited with code ${code} — log: ${logPath}`)
    }
  })

  let stubServer: Server | undefined

  try {
    let up = false
    for (let i = 0; i < 150 && !up; i++) {
      if (spawnError || child.exitCode !== null) break
      try {
        const res = await fetch(`${baseUrl}/healthz`, { signal: AbortSignal.timeout(2_000) })
        up = res.ok
      } catch {
        // not listening yet (or the request stalled past the 2s abort timeout)
      }
      if (!up) await sleep(200)
    }
    if (!up) {
      if (spawnError) throw new Error(`failed to spawn ${bin}: ${spawnError.message} — log: ${logPath}`)
      throw new Error(`server did not become healthy on ${baseUrl} — see ${logPath}`)
    }

    const db = new DatabaseSync(dbPath)
    // The server holds the same file open in WAL mode; give our reads/writes the
    // same busy timeout it uses (PRAGMA via the Go DSN is per-connection).
    db.exec('PRAGMA busy_timeout = 5000')

    const stub = await startGotifyStub()
    stubServer = stub.server

    // Teardown must be exception-safe: a throw from db.close()/stub close must
    // not skip the child kill and temp-dir removal, or a leaked server holds its
    // port and DATA_DIR for the rest of the run.
    const stop = async () => {
      try {
        try {
          db.close()
        } catch (err) {
          console.error(`[e2e] teardown: db.close failed — ${String(err)}`)
        }
        try {
          stub.server.close()
        } catch (err) {
          console.error(`[e2e] teardown: stub close failed — ${String(err)}`)
        }
      } finally {
        try {
          if (child.exitCode === null) {
            child.kill('SIGTERM')
            const deadline = Date.now() + 5_000
            while (child.exitCode === null && Date.now() < deadline) await sleep(50)
            if (child.exitCode === null) child.kill('SIGKILL')
          }
        } finally {
          logStream.end()
          if (process.env.E2E_KEEP_DATA === '1') {
            console.log(`E2E_KEEP_DATA=1 — keeping ${dataDir} (server log: ${logPath})`)
          } else {
            await rm(dataDir, { recursive: true, force: true })
          }
        }
      }
    }

    return {
      app: {
        baseUrl, dataDir, dbPath, db, logPath,
        stubUrl: stub.url, stubMessages: stub.messages, stubServer: stub.server,
      },
      stop,
    }
  } catch (err) {
    // Startup failed after spawn: without this the child keeps running and holds
    // both its port and the temp DATA_DIR forever.
    stubServer?.close()
    if (child.exitCode === null) {
      child.kill('SIGTERM')
      const deadline = Date.now() + 5_000
      while (child.exitCode === null && Date.now() < deadline) await sleep(50)
      if (child.exitCode === null) child.kill('SIGKILL')
    }
    logStream.end()
    if (process.env.E2E_KEEP_DATA === '1') {
      console.log(`E2E_KEEP_DATA=1 — keeping ${dataDir} (server log: ${logPath})`)
    } else {
      await rm(dataDir, { recursive: true, force: true })
    }
    throw err
  }
}

type Session = {
  pageAs(email?: string, opts?: BrowserContextOptions): Promise<Page>
  apiAs(email?: string): Promise<APIRequestContext>
}

export const test = base.extend<{ app: App; session: Session }, { app: App }>({
  app: [
    async ({}, use) => {
      const { app, stop } = await startApp()
      try {
        await use(app)
      } finally {
        await stop()
      }
    },
    { scope: 'worker', timeout: 120_000 },
  ],

  session: async ({ app, browser }, use) => {
    // Never close a context mid-test: pageAs(email, opts) and apiAs(email) may
    // both be live in one test, in either order. Distinct opts get their own
    // cached context; everything is closed once, at fixture teardown.
    const cache = new Map<string, BrowserContext>()
    const contexts = new Set<BrowserContext>()
    const ctxFor = async (email: string, opts?: BrowserContextOptions) => {
      const key = opts ? `${email}|${JSON.stringify(opts)}` : email
      const hit = cache.get(key)
      if (hit) return hit
      const ctx = await browser.newContext({
        ...opts,
        baseURL: app.baseUrl,
        extraHTTPHeaders: { 'X-Dev-Email': email },
      })
      await ctx.addInitScript(
        (email: string) => localStorage.setItem('wiminder-dev-email', email),
        email,
      )
      cache.set(key, ctx)
      contexts.add(ctx)
      return ctx
    }
    await use({
      pageAs: async (email = MEMBER_A, opts) => (await ctxFor(email, opts)).newPage(),
      apiAs: async (email = MEMBER_A) => (await ctxFor(email)).request,
    })
    for (const c of contexts) await c.close()
  },
})

export { expect }
