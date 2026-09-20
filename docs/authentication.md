# Authentication

wiminder ships two auth modes, selected with `AUTH_MODE`: `cfaccess` (production, default) and `dev` (local only).

## Cloudflare Access (production)

1. Zero Trust → **Access** → **Applications** → **Add an application** → **Self-hosted**.
2. Domain: `wiminder.your-domain.com` (the subdomain used by the tunnel).
3. Add a policy: Action **Allow**, Include **Emails** → your email (and family members').
4. Note the two values from the Access application:
   - **Team domain**: `your-team.cloudflareaccess.com` → `CF_ACCESS_TEAM_DOMAIN`
   - **Application Audience (AUD) tag** → `CF_ACCESS_AUD`
5. Create the tunnel: Zero Trust → **Networks** → **Tunnels** → **Create a tunnel** (Cloudflared) → copy **TUNNEL_TOKEN** into `.env`.
6. Add a **Public hostname** to the tunnel: `wiminder.your-domain.com` → Service `http://app:8080`.
7. Fill in `.env` (`CF_ACCESS_TEAM_DOMAIN`, `CF_ACCESS_AUD`, `ADMIN_EMAILS`) and run `docker compose --profile cloudflared up -d`.

The application validates the Cloudflare Access JWT (JWKS is cached); the email from the JWT claim is used to auto-provision users, and emails listed in `ADMIN_EMAILS` are granted the admin role.

## Dev mode (no tunnel)

To try it locally without Cloudflare Access:

```bash
AUTH_MODE=dev make run
```

Open `http://localhost:8080`; when prompted, enter any email (e.g. `admin@local.test`). Technically, dev mode reads the `X-Dev-Email` header — handy for curl:

```bash
curl -H 'X-Dev-Email: admin@local.test' http://localhost:8080/api/v1/upcoming
```

Do not use `AUTH_MODE=dev` on a publicly exposed instance.
