# Ops runbook — go-live checklist

Single-VPS deploy: Go API + self-hosted Postgres (PostGIS + TimescaleDB +
pgvector). CI builds images → GHCR → SSH deploy. See `deploy/README.md` for the
architecture; this is the operator checklist.

## 0. Prerequisites
- An **x86_64** VPS (the Postgres image is amd64-only).
- A domain for the API, with DNS pointing at the VPS.
- A GitHub PAT with `read:packages` (for the VPS to pull GHCR images).

## 1. GitHub repo secrets (Settings → Secrets and variables → Actions)
| Secret | Value |
|---|---|
| `VPS_HOST` | VPS IP / hostname |
| `VPS_USER` | SSH user |
| `VPS_SSH_KEY` | private SSH key for that user |
| `VPS_SSH_PORT` | SSH port (optional, default 22) |
| `VPS_APP_DIR` | app dir on the VPS, e.g. `/opt/loci` |
| `GHCR_TOKEN` | PAT with `read:packages` (VPS image pulls) |

Image **push** uses the built-in `GITHUB_TOKEN` (workflow already has `packages: write`).

## 2. One-time VPS setup
```bash
curl -fsSL https://get.docker.com | sh
sudo mkdir -p /opt/loci && sudo chown "$USER" /opt/loci && cd /opt/loci
echo "<GHCR_READ_PAT>" | docker login ghcr.io -u <github-user> --password-stdin
# create the prod .env (next step) before the first deploy
```

## 3. Production `.env` (in `VPS_APP_DIR`, from `.env.prod.example`)
Required:
- `APP_ENV=production`
- `JWT_SECRET` — 32+ random bytes (the app refuses to boot with `changeme` in prod)
- `ALLOWED_ORIGINS` — the web app origin(s), comma-separated (CORS)
- `AI_PROVIDER=openrouter`, `OPENROUTER_API_KEY`, and `OPENROUTER_MODEL`
- `AI_FALLBACK_ENABLED=false` — the free-tier fallback is a local testing floor. Production fails loudly and pages when the provider is out of credits, rather than degrading paying users onto shared rate-limited models. The server refuses to boot if this is true while `APP_ENV=production`.

### Live trip signals

Public holidays, natural hazards, air quality and exchange rates. Every source is free and
keyless, so the whole feature works with nothing configured — `SIGNALS_ENABLED=false` turns
it all off.

- **Nothing here may fail a request.** Each source is bounded at 3s, the whole fan-out at
  10s, and a source that fails twice consecutively is benched for 5 minutes. Watch
  `loci_external_source_benched_total`: a source benching repeatedly is a provider outage,
  and it will show as *missing* trip context rather than as an error, because these adapters
  degrade silently by design.
- **`loci_external_requests_total{source,outcome}`** is how you tell a dead provider from a
  quiet destination. A source whose `ok` count falls to zero while the app keeps serving has
  disappeared, and nothing else will say so.
- **`loci_external_cache_hits_total{source}`** is the quota guard. These are free tiers with
  rate limits; a hit ratio falling towards zero means a cache key became too specific and
  the provider is about to start refusing us.
- **`GDELT_ENABLED` defaults to off and should stay off** unless someone has looked at the
  output. Measured: 23-26s for a query that completes, 60s+ timeouts for anything precise
  enough to be useful, and a live query for French transport strikes returned a hunger
  strike, Russian airstrikes and a football match. It never blocks a request (it serves
  cache and refreshes in the background), but it is decoration, not information.
- **Open-Meteo's free tier is non-commercial.** It is the default weather and air-quality
  provider and is correct for validation; a commercial deployment needs their paid tier or
  `WEATHER_PROVIDER=openweather`.
- Set `REDIS_URL` to share provider caches across replicas. Without it each replica keeps
  its own in-memory copy and the outbound request rate multiplies by the replica count.
  (or the documented Gemini alternative)
- `STRIPE_API_KEY`, `STRIPE_WEBHOOK_SECRET`
- `DB_USER/DB_PASSWORD/DB_NAME` **must equal** `POSTGRES_USER/POSTGRES_PASSWORD/POSTGRES_DB`
- `BASE_URL` — public API URL (used for share links / OG tags)

> `SERVER_HOST`, `DB_HOST`, `DB_PORT` are set by `docker-compose.prod.yaml`; don't set them in `.env`.

## 4. Stripe setup
- Create products + recurring **prices** for `premium_monthly` (interval `month`) and `premium_annual` (interval `year`). The webhook handler maps the price interval → the `subscription_plan_type` enum, so the interval must be correct.
- Add a webhook endpoint → `https://<api-domain>/webhooks/stripe`; copy its signing secret into `STRIPE_WEBHOOK_SECRET`.
- Subscribe at least to `customer.subscription.{created,updated,deleted}` and `invoice.payment_succeeded`.

## 5. TLS / reverse proxy
API is published on `127.0.0.1:8080` only. Front it with Caddy/nginx:
```
api.your-domain.com {
    reverse_proxy 127.0.0.1:8080
}
```

## 6. First deploy
- Push to `main` (CI must pass) → `cd.yml` auto-deploys. Or tag `vX.Y.Z` for an explicit release.
- Verify: `curl https://api.your-domain.com/health` → `ok`; `/health/details` → all `ok`.
- Check `docker compose -f docker-compose.prod.yaml logs -f api` for `registered Connect RPC service` lines (auth, chat, poi, list, review, payment, share, …).

## 7. Monitoring
- Liveness: `GET /health`, readiness `GET /ready`.
- Metrics: `GET /metrics` (Prometheus; enabled when `METRICS_ENABLED=true`). Scrape it; alert on error rate + latency.

## 8. Backups (you own the DB)
```bash
# nightly cron
docker exec loci-postgres pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB" \
  | gzip > /opt/loci/backups/loci-$(date +%F).sql.gz
# copy offsite (s3/rsync). Data lives in the postgres-data named volume.
```

## 9. Rollback
```bash
cd /opt/loci
export API_IMAGE=ghcr.io/<owner>/loci-connect-api:<previous-tag>
export POSTGRES_IMAGE=ghcr.io/<owner>/loci-connect-api-postgres:<previous-tag>
docker compose -f docker-compose.prod.yaml up -d
```
Migrations are forward-only (goose up); a code rollback does **not** revert
schema — avoid destructive migrations or pair them with a DB restore.

## 10. Known follow-ups
- Reviews global feed (`GetRecentReviews`) needs a proto regen + BSR publish (`buf registry login` → `make generate && make push` in `loci-connect-proto`, then `pnpm buf-update` in the client). Until then the reviews "All" tab is reviewer/POI-enriched per-POI + my-reviews only.
