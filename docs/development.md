# Development

## Make targets

```bash
make test    # CGO_ENABLED=0 go test ./... -count=1
make dev     # backend with hot reload (air): rebuild + restart on .go changes
make web     # pnpm install --frozen-lockfile + build SPA → internal/api/webroot (embed)
make build   # build SPA + binary to bin/wiminder
make run     # build + run dev mode on :8080 (without hot reload)
make e2e     # build + run the Playwright e2e suite (web/e2e, chromium)
make container  # docker compose build, podman-compose fallback (Makefile)
```

## E2E suite

The e2e suite spawns a real `bin/wiminder` per worker (`AUTH_MODE=dev`, fresh SQLite under a temp `DATA_DIR`) and asserts against the database file directly. First run needs `cd web && pnpm exec playwright install chromium`; debug a failure with `E2E_KEEP_DATA=1 make e2e` (keeps the temp dir + server log) and `cd web && pnpm run test:e2e:report`.

## Full-stack dev flow

Two terminals: `make dev` for the backend (air, ~1s auto-rebuild) and `cd web && pnpm run dev` for the frontend (Vite HMR, proxying `/api` to `:8080`). Air is pinned via the `tool` directive in go.mod — no manual install needed, just `go tool air`. Configuration lives in `.air.toml` (only non-test `.go` files trigger a rebuild; the SPA still goes through Vite).

## Landing page (`site/`)

The public marketing page deployed to GitHub Pages at `https://dharmasaputraa.github.io/wiminder/`. Standalone Vite + React + Tailwind package with its own lockfile (not a workspace with `web/`, and not embedded in the binary):

```bash
cd site
pnpm install
pnpm dev       # dev server at http://localhost:5173/wiminder/ (HMR)
pnpm lint      # oxlint
pnpm build     # tsc --noEmit + vite build → site/dist
pnpm preview   # serve site/dist at http://localhost:4173/wiminder/
```

Deploy is automatic via `.github/workflows/pages.yml` on pushes that touch `site/**`. One-time repo setup: Settings → Pages → Source: **GitHub Actions**.

Screenshots on the landing page are generated from the real app by `web/e2e/site-screenshots.mjs` (dev server + seeded demo data):

```bash
make build && cd web && node e2e/site-screenshots.mjs
```

## Pawukon fixtures

Pawukon fixtures are scraped once at dev time (not at runtime) with a separate module:

```bash
cd scripts/fetch_fixtures && go run . -year 2026 -out ../../testdata
```

Fixture data © [kalenderbali.org](https://kalenderbali.org) (I Wayan Nuarsa, Universitas Udayana) — used as personal test fixtures with attribution; **do not redistribute**. wiminder at runtime never depends on third-party sites.

**Remote holiday provider note:** the `dayoffapi` and `kresnasatya` providers use cache-first with a 10-minute negative cache — if the remote service is down, wiminder stops trying temporarily and uses the existing cache. Local Pawukon/otonan calculation keeps working in full; only national holidays are temporarily empty.

## Container verification

**Verified with Podman 6.0.2 + podman-compose 1.6.0** on the developer's machine: `podman build -t wiminder:latest .` succeeds (39.7 MB image), smoke containers pass (healthz, SPA, deep-link, scheduler-run). Podman note: HEALTHCHECK is ignored with the OCI format — add `--format docker` to `podman build` if you want the healthcheck. The following steps remain relevant for Docker users:

The current development environment has no Docker, so the following steps must be run manually on a machine that does:

```bash
cp .env.example .env   # set at least APP_SECRET
docker compose config                        # validate compose, no errors
docker compose build app                     # image builds
docker run --rm -d --name wiminder-smoke -p 8081:8080 \
  -e APP_SECRET=dev-secret-long-16 -e AUTH_MODE=dev -e ADMIN_EMAILS=a@b.c wiminder-app:latest
sleep 2
curl -s localhost:8081/healthz               # expect: {"ok":true}
curl -s localhost:8081/ | head -c 120        # expect: SPA HTML (<!doctype html> / <div id="root">)
curl -s -o /dev/null -w '%{http_code}\n' localhost:8081/contacts/1   # expect: 200 (SPA deep-link)
curl -s -H 'X-Dev-Email: a@b.c' -X POST localhost:8081/api/v1/scheduler/run   # expect: {"sent":...,"failed":...,"missed":...}
docker rm -f wiminder-smoke
```

Note: `ADMIN_EMAILS` must be included because `/scheduler/run` is admin-only. The image name produced by `docker compose build app` follows the project directory name (e.g. `wiminder-app` if the repo is in a folder named `wiminder`); if it differs, adjust the tag or build with `docker build -t wiminder-app .`.

Verification without Docker is still possible via `make test` + `make build` + `make run` (see §Make targets).
