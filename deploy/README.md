# Production deploy — self-hosted Postgres + Go API on a VPS

Single-VPS setup. CI builds two images (API + the PostGIS/TimescaleDB/pgvector
Postgres), pushes them to GHCR, then SSHes into the VPS and runs
`docker compose pull && up -d`. The API runs migrations on boot.

## Architecture

```
GitHub tag v* ──> Actions (build.yml=deploy.yml)
                   ├─ build & push ghcr.io/<repo>            (API)
                   ├─ build & push ghcr.io/<repo>-postgres   (DB)
                   └─ ssh VPS: docker compose pull && up -d
VPS
  ├─ loci-api       :8080 (bound to 127.0.0.1) ── put nginx/Caddy in front for TLS
  ├─ loci-preference-rerank  recomputes taste vectors every 15 minutes
  └─ loci-postgres  internal network only, named volume postgres-data
```

> The Postgres image is amd64-only (postgis base). The VPS must be x86_64.

## One-time VPS setup

```bash
# 1. Install Docker Engine + compose plugin (Ubuntu)
curl -fsSL https://get.docker.com | sh

# 2. App directory (must match the VPS_APP_DIR secret)
sudo mkdir -p /opt/loci && sudo chown "$USER" /opt/loci
cd /opt/loci

# 3. Production env — copy .env.prod.example -> .env and fill it in
#    (DB_* must match POSTGRES_*; set real JWT_SECRET + GEMINI_API_KEY)
vim .env

# 4. Log in to GHCR so the VPS can pull private images
echo "<GHCR_READ_PAT>" | docker login ghcr.io -u <github-user> --password-stdin
```

The compose file is copied to the VPS by the workflow, so it does not need to be
placed manually — but the **`.env` must already exist** before the first deploy.

## Required GitHub secrets

| Secret          | Purpose                                                        |
|-----------------|----------------------------------------------------------------|
| `VPS_HOST`      | VPS hostname / IP                                              |
| `VPS_USER`      | SSH user                                                       |
| `VPS_SSH_KEY`   | Private SSH key for that user                                  |
| `VPS_SSH_PORT`  | SSH port (optional, defaults to 22)                           |
| `VPS_APP_DIR`   | App dir on the VPS, e.g. `/opt/loci`                          |
| `GHCR_TOKEN`    | PAT with `read:packages` so the VPS can pull the images        |

Image push uses the built-in `GITHUB_TOKEN` (needs `packages: write`, already set).

## Deploy

**Automatic (default):** every push to `main` runs CI; when CI passes, `cd.yml`
builds the images at that commit and deploys. A red CI never deploys.

**Explicit release:** tag a version to deploy that exact ref.
```bash
git tag v1.0.0 && git push origin v1.0.0      # triggers build + deploy
```
Or run the **Deploy (release)** workflow manually (workflow_dispatch).

Both paths share `.github/workflows/_deploy.yml` (build → push GHCR → SSH deploy).

## TLS / reverse proxy

The API is published on `127.0.0.1:8080` only. Terminate HTTPS with Caddy or
nginx on the host, proxying to `http://127.0.0.1:8080`. Minimal Caddy:

```
api.your-domain.com {
    reverse_proxy 127.0.0.1:8080
}
```

## Operations

```bash
cd /opt/loci
docker compose -f docker-compose.prod.yaml ps
docker compose -f docker-compose.prod.yaml logs -f api
docker compose -f docker-compose.prod.yaml logs -f preference-rerank
curl -s localhost:8080/health            # -> ok
```

### Database backups (you own the box now)

```bash
# dump
docker exec loci-postgres pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB" \
  | gzip > /opt/loci/backups/loci-$(date +%F).sql.gz
```
Add a cron job + offsite copy. The data lives in the `postgres-data` named volume.

## Rollback

```bash
export API_IMAGE=ghcr.io/<repo>:<previous-tag>
export POSTGRES_IMAGE=ghcr.io/<repo>-postgres:<previous-tag>
docker compose -f docker-compose.prod.yaml up -d
```
Note: migrations are forward-only (goose up); a code rollback does not roll back
schema. Avoid destructive migrations or pair them with a DB restore plan.
