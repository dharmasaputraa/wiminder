# Deploying to Dokploy — Setup Guide

Versioning flow: **git tag → GitHub Actions builds the image → push to GHCR → Dokploy pulls & runs it**.
The server never builds — all builds happen in GitHub Actions.

```
git tag v1.2.3 && git push origin v1.2.3
        │
        ▼
GitHub Actions (release.yml)
  go test → buildx multi-arch (amd64+arm64)
        │
        ▼
ghcr.io/dharmasaputraa/wiminder
  tags: 1.2.3 · 1.2 · 1 · latest · sha-xxxxxxx
        │  (API call: POST /api/application.deploy, pinned to sha-xxxxxxx)
        ▼
Dokploy (pull the pinned tag → new container → healthz → routing)
```

Published images can be viewed at: `github.com/dharmasaputraa/wiminder/pkgs/container/wiminder`.

---

## Step 1 — GitHub: create a PAT for GHCR

Dokploy needs a token to **pull** images from GHCR.

1. Open <https://github.com/settings/tokens> → **Generate new token (classic)**.
2. Give it a note, e.g. `dokploy-ghcr-read`.
3. Check the **`read:packages`** scope (enough for pulling; use `write:packages` only if you want Dokploy to be able to push too).
4. Generate, **copy the token** (shown only once).

## Step 2 — Dokploy: register the GHCR Registry

1. Dokploy panel → **Registry** menu → choose **GHCR**.
2. Fill in:
   - **Registry Name**: `ghcr` (free choice)
   - **Username**: `dharmasaputraa`
   - **Password/Token**: the PAT from Step 1
   - **Registry URL**: `ghcr.io`
3. Click **Test** (must succeed) → **Create**.

## Step 3 — Dokploy: create the Application

1. Project → **Create Service** → **Application**.
2. **General** tab → Source Type: **Docker**.
3. **Docker Image**: `ghcr.io/dharmasaputraa/wiminder:latest`
4. Pick the `ghcr` registry registered in Step 2 (or fill in the registry URL + credentials manually).
5. **Save** — don't Deploy yet; first complete Environment & Volume (Steps 4–5).

> After the first release, the GHCR package is still **private** (default). As long as the registry from Step 2 is correct, pulls keep working. Want to pull without credentials? Change the package visibility to public via the Packages page on GitHub.

## Step 4 — Environment variables (Environment tab)

| Variable | Required | Value / example |
| --- | --- | --- |
| `APP_SECRET` | ✅ | random, min. 16 chars — `openssl rand -base64 32`. Encryption key for notification channels; **never change it** once data exists |
| `AUTH_MODE` | ✅ | `cfaccess` |
| `CF_ACCESS_TEAM_DOMAIN` | ✅ | `your-team.cloudflareaccess.com` (see Step 7) |
| `CF_ACCESS_AUD` | ✅ | the Access application's AUD tag (see Step 7) |
| `ADMIN_EMAILS` | ✅ | admin emails, comma-separated |
| `TZ` | optional | `Asia/Makassar` (log timezone; reminder schedules are configured from the UI Settings) |

What you **don't** need to set (already defaulted by the image / dev only):
`DATA_DIR` (default `/data`), `ADDR` (default `:8080`), `APP_PORT` & `TUNNEL_TOKEN` (only for manual docker-compose on a VPS), `DEV_SEED_*` (only for `AUTH_MODE=dev`).

## Step 5 — Volume (Volumes tab) — REQUIRED

SQLite is stored in `/data`. **Without a volume, all data is lost on every redeploy/restart.**

- Mount: `/data` → a named volume (e.g. `wiminder-data`) or a host path (e.g. `/var/lib/wiminder`).

## Step 6 — Domain (Domains tab)

1. Add domain → point it at **port 8080** (the app's internal port).
2. DNS: A/CNAME the domain → the VPS, with **Cloudflare proxy ON** (orange).
3. HTTPS: let Dokploy handle it (Traefik + Let's Encrypt), or set Full (strict) if terminating at Cloudflare.

## Step 7 — Cloudflare Access (app authentication)

The app validates the Cloudflare Access JWT (`AUTH_MODE=cfaccess`), so the domain must be protected by Access:

1. Cloudflare Zero Trust → **Access → Applications → Add → Self-hosted**.
2. Application domain: the app domain from Step 6.
3. Policy: **Allow** → Include → Emails → same list as `ADMIN_EMAILS`.
4. From the Access application page, copy the **Team domain** (`xxx.cloudflareaccess.com`) and **AUD tag** → fill them into the `CF_ACCESS_TEAM_DOMAIN` & `CF_ACCESS_AUD` env vars in Step 4.

More details: README §Cloudflare Access setup.

## Step 8 — Health check

The image ships with a built-in `HEALTHCHECK` (GET `/healthz`, port 8080, every 30s). If the **Advanced** tab of the Dokploy application offers a health check/restart option, set path `/healthz` port `8080` — if not, the image default is enough.

## Step 9 — Hook up CI/CD (once)

1. **Dokploy API key**: avatar/profile → **API Keys** → create new → copy (shown only once).
2. **Application ID**: open the application in the panel — the ID is in the URL, `.../service/<applicationId>`.
3. **GitHub secrets** (repo → Settings → Secrets and variables → Actions → New repository secret):

   | Secret | Value |
   | --- | --- |
   | `DOKPLOY_URL` | `https://your-dokploy-panel.com` (no trailing slash) |
   | `DOKPLOY_API_KEY` | API key from step 1 |
   | `DOKPLOY_APPLICATION_ID` | ID from step 2 |

   Without these secrets the release workflow **still** builds & pushes the image — only the auto-redeploy step is skipped.

   Optional: add `APP_HEALTH_URL` (the production app's base URL) and the workflow will wait for `/healthz` to return 200 after deploying, failing the run if it doesn't.

## Step 10 — First release & verification

```bash
git tag v0.1.0 && git push origin v0.1.0
```

1. Watch the **Actions** tab on GitHub (the *Release* workflow, ± 3–6 minutes for multi-arch).
2. The image appears on the repo's **Packages** page.
3. Dokploy redeploys automatically — check the **Deployments** tab of the application.
4. Verify:
   ```bash
   curl https://your-domain.com/healthz     # {"ok":true}
   ```
   Then open the domain → log in through Cloudflare Access → the app shows up.

---

## Day-to-day operations

**Release a new version**: `git tag v1.2.4 && git push origin v1.2.4` — done.

### Building the image locally (optional)

The main build path is GitHub Actions — the server **never builds** (it only pulls).
For experiments/quick hotfixes, the image can be built on a local machine (Podman, no Docker needed):

```bash
make test                                        # local builds don't pass the CI gate — test manually first
podman build --platform linux/amd64 \
  -t ghcr.io/dharmasaputraa/wiminder:0.1.0 . # match the server arch (uname -m)
podman run --rm -d --name smoke -p 8081:8080 \
  -e APP_SECRET=dev-secret-long-16 -e AUTH_MODE=dev -e ADMIN_EMAILS=a@b.c \
  ghcr.io/dharmasaputraa/wiminder:0.1.0
curl -s localhost:8081/healthz && podman rm -f smoke   # smoke test (README §Container verification)
echo "<TOKEN>" | podman login ghcr.io -u dharmasaputraa --password-stdin
podman push ghcr.io/dharmasaputraa/wiminder:0.1.0
```

- The PAT for pushing must have **`write:packages`** (read-only is not enough).
- After pushing: change the tag in Dokploy → Deploy, or trigger `POST /api/application.deploy` like CI does.
- Note: local builds skip CI tests and cross-arch builds on a Mac run via emulation (slower) — use only for hotfixes; official releases still go through `git tag`.

**Rollback**: the **General** tab of the Dokploy application → change the image tag to an older version (e.g. `ghcr.io/dharmasaputraa/wiminder:1.2.2`) → **Deploy**. All versions are kept in GHCR.

**Redeploy the same version**: the **Deploy** button in the panel, or re-run the Release workflow via *Run workflow* (manual dispatch).

## Backup

The only state is the SQLite file in `/data` (`wimember.db`). Principle: backups must live **outside the server** — VPS dies = everything is lost. A practical target: Cloudflare R2 (free 10 GB; this db is only a few MB).

> Tip: mount the `/data` volume as a **host path** (e.g. `/opt/wiminder/data:/data`), not a named volume — the file is directly visible on the host and easy to back up.

### Option A — Cron + rclone (simple, good starting point)

Daily backup using sqlite3's built-in `.backup` — safe to run while the app keeps running (WAL-safe):

```bash
apt install -y sqlite3 rclone          # or apk add on alpine
rclone config                          # once: create a remote, e.g. named "r2" (R2 endpoint + access key)
mkdir -p /opt/wiminder/backups
crontab -e
# add (single line; \% is escaped for cron):
30 2 * * * sqlite3 /opt/wiminder/data/wimember.db ".backup '/opt/wiminder/backups/wiminder-$(date +\%F).db'" && find /opt/wiminder/backups -name 'wiminder-*.db' -mtime +14 -delete && rclone copy /opt/wiminder/backups r2:wiminder-backup --max-age 48h
```

**Restore**: stop the app in Dokploy → overwrite `/opt/wiminder/data/wimember.db` with the backup file → Start → check `/healthz`.

### Option B — Litestream (continuous, point-in-time recovery)

Real-time replication to S3/R2; a config is available in the repo (`deploy/litestream.yml`):

1. Create a bucket + access key (R2: also set `LITESTREAM_ENDPOINT`), adjust the replica URL in `deploy/litestream.yml`.
2. Upload `deploy/litestream.yml` to the host, then in Dokploy create a **second service**: image `litestream/litestream`, command `replicate -config /etc/litestream.yml`, mount the same host path (`/opt/wiminder/data:/data`) and the config read-only (`/opt/wiminder/litestream.yml:/etc/litestream.yml:ro`), env `LITESTREAM_ACCESS_KEY_ID` / `LITESTREAM_SECRET_ACCESS_KEY` / `LITESTREAM_ENDPOINT`.
3. **Restore**: stop the app → `litestream restore -o /data/wimember.db s3://bucket/wiminder/wimember.db` → start.

Start the new Litestream after the db has content (or do one initial backup via Option A) so replication has a baseline.

## Troubleshooting

| Symptom | Common cause |
| --- | --- |
| **Host Error** badge on the Domains tab (while the app runs) | Cosmetic: Dokploy's DNS validation compares the resolved IP vs the server IP — domains proxied by Cloudflare always fail this check. **Ignore it**; don't switch the record to DNS-only to make it disappear (Access needs the proxy, and the VPS IP would be exposed) |
| Deploy fails to pull `manifest unknown` | The tag isn't on GHCR yet — the Actions build isn't finished / the tag is mistyped |
| Deploy fails to pull `denied` | Private package + wrong registry credential / PAT without `read:packages` |
| Deploy API 401/403 | Wrong `DOKPLOY_API_KEY` or `DOKPLOY_APPLICATION_ID` |
| Deploy API never arrives (timeout) | Cloudflare in front of the panel blocks the POST — disable Bot Fight Mode / create an allow rule for the `/api/*` path |
| App error `APP_SECRET required` | Env not filled in on the Environment tab |
| Contact data lost after redeploy | The `/data` volume isn't mounted (Step 5) |
| Redirect loop / 403 from Access | `CF_ACCESS_TEAM_DOMAIN` / `CF_ACCESS_AUD` don't match the Access application |
