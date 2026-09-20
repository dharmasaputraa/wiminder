# wiminder

**wiminder** is a self-hosted reminder service for Balinese otonan based on the 210-day Pawukon cycle, birthdays, anniversaries, and holidays — computed automatically and delivered via Gotify, Telegram, or email. Everything runs from a single container: the SPA is embedded in the Go binary, the SQLite database lives on a volume, and access is secured through Cloudflare Access with no additional password.

## Features

- **Otonan & pawukon** — the 210-day otonan cycle is computed from the date of birth; labels include saptawara, pancawara, and wuku.
- **Other event types** — birthdays (including Feb 29), anniversaries, and standard Pawukon holidays (Galungan, Kuningan, Saraswati, Pagerwesi).
- **National & Saka holidays** — categories can be toggled; remote data is cached in SQLite.
- **Customizable offsets** — defaults are D-7, D-4, D-2, D-1, D+0; each contact can have its own offsets.
- **Deduplication** — a unique constraint in the notification log guarantees each reminder is sent once per event/date/offset/channel.
- **Catch-up** — reminders missed while the container was down are still sent (default window: 24 hours) and labeled "late".
- **Multi-channel** — Gotify (self-hosted), Telegram, and SMTP/email; channel configs are encrypted with AES-256-GCM using `APP_SECRET`.
- **Test send** — a per-channel test button on the Channels page.
- **PWA** — manifest + service worker, installable from a phone.
- **Cross-year calendar** — ±1 year navigation arrows in the calendar; data is fetched per year (`/upcoming?from=&to=`, max 400 days) and the surrounding ±1 year is prefetched.
- **Observability** — `/healthz`, `/readyz`, and `/metrics` (Prometheus).
- **Send time & timezone** — defaults to 08:00 and `Asia/Makassar` (WITA), configurable in the Settings UI (e.g. `Asia/Jakarta` for WIB).

## Quickstart

You need Docker (+ Compose) **or** Podman (+ podman-compose), and a domain pointed at Cloudflare (for the tunnel).

```bash
git clone <your-repo> wiminder && cd wiminder
cp .env.example .env   # set APP_SECRET, CF_ACCESS_*, ADMIN_EMAILS
podman-compose up -d                 # app only; no profile needed if cloudflared already runs on the host
# (Docker users: docker compose up -d --build; need an in-container tunnel: add --profile cloudflared)
```

The application only listens on the internal compose network; public access goes through the Cloudflare tunnel. Open `https://wiminder.your-domain.com`. To try it locally without Cloudflare Access: `AUTH_MODE=dev make run` → `http://localhost:8080` (details in [docs/authentication.md](docs/authentication.md)).

Compose profiles (all optional, `app` is always included):

| Profile | Contents | Command |
|---|---|---|
| `cloudflared` | in-container tunnel (requires `TUNNEL_TOKEN`) — **skip if cloudflared already runs on the host** | `docker compose --profile cloudflared up -d` |
| `gotify` | self-hosted Gotify (`http://gotify:80`) | `docker compose --profile gotify up -d` |
| `litestream` | SQLite replication to S3/R2 | `docker compose --profile litestream up -d` |

**Cloudflared on the host (common setup):** the app publishes port `APP_PORT` (default `8080`) to the host — point your cloudflared tunnel at `http://localhost:8080`. Authentication still comes from Cloudflare Access on the Cloudflare side, not from the container.

> **v0.x breaking change:** IDs are now UUIDv7 and occasions gained recurrence — delete your old `data/wimember.db` (schema is incompatible); take a backup first if needed.

### Environment

| Variable | Required | Default | Description |
|---|---|---|---|
| `APP_SECRET` | yes | — | AES-256-GCM key for channel configs, at least 16 characters |
| `AUTH_MODE` | yes | `cfaccess` | `cfaccess` (production) or `dev` (no tunnel) |
| `CF_ACCESS_TEAM_DOMAIN` | cfaccess mode | — | `your-team.cloudflareaccess.com` |
| `CF_ACCESS_AUD` | cfaccess mode | — | Application Audience tag from Access |
| `ADMIN_EMAILS` | recommended | — | Admin emails, comma-separated; admins can see all contacts and run the scheduler manually |
| `TZ` | no | `Asia/Makassar` | Container process timezone (logs). **The schedule timezone and reminder send time are configured in the Settings UI** (default `Asia/Makassar`; change to `Asia/Jakarta` for WIB) |
| `DATA_DIR` | no | `/data` (image) | SQLite file location |
| `ADDR` | no | `:8080` | Listen address |
| `TUNNEL_TOKEN` | cloudflared profile | — | Cloudflare tunnel token |

## Documentation

- **[Authentication](docs/authentication.md)** — Cloudflare Access setup (production) and dev mode.
- **[Notification channels](docs/notifications.md)** — Gotify, Telegram, and SMTP/email setup.
- **[Backup & restore](docs/backup-restore.md)** — Litestream replication to S3/R2 and manual restore.
- **[Development](docs/development.md)** — make targets, full-stack dev flow, e2e suite, landing page (`site/`), pawukon fixtures, container verification.
- **[Deployment with Dokploy](docs/dokploy-setup.md)** — registry, application, volumes, CI wiring, backup, troubleshooting.

## CI/CD & Deployment

Images are **built in GitHub Actions and pushed to GHCR** — Dokploy only pulls pre-built images, so versioning happens via image tags (no build load on the server).

| Workflow | Trigger | What it does |
| --- | --- | --- |
| `ci.yml` | push to `main`, PRs | `go test` + `go vet`, web lint + typecheck/build, full Docker image build (no push) |
| `release.yml` | push tag `v*` (or manual) | `go test`, multi-arch image → `ghcr.io/dharmasaputraa/wiminder`, redeploy via Dokploy API |

Release: `git tag v1.2.3 && git push origin v1.2.3` — pushes `1.2.3`, `1.2`, `1`, `latest`, `sha-<sha>` (linux/amd64 + arm64) and triggers the Dokploy redeploy. Rollback: point the application's image tag at an older version in Dokploy → Deploy.

## Project structure

```
code/
├── cmd/server/main.go          # wiring: config, db, router, scheduler, notifiers
├── internal/
│   ├── domain/                 # PURE: pawukon, occurrence, pawukon holidays, offsets
│   ├── store/                  # SQLite: embedded migrations, repository per table
│   ├── notify/                 # Notifier + gotify.go, telegram.go, smtp.go
│   ├── scheduler/              # ticker, Clock, dedupe, catch-up
│   ├── api/                    # Gin handlers, cfaccess middleware, embedded static
│   └── calendarprov/           # HolidayProvider + computed/remote impls
├── scripts/fetch_fixtures/     # kalenderbali.org scraper → testdata/*.csv (separate module)
├── web/                        # Vite + React SPA; build output → internal/api/webroot (embed)
├── site/                       # Vite + React landing page → GitHub Pages (separate from web/)
├── deploy/                     # litestream.yml, example deploy config
├── testdata/                   # pawukon CSV fixtures (kalenderbali.org, do not redistribute)
├── Dockerfile                  # multi-stage: node build → go build → alpine (verified with podman)
├── docker-compose.yml          # app + cloudflared/gotify/litestream profiles
└── docs/                       # topic docs + superpowers specs (design documents)
```
