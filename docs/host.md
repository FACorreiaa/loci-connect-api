# Server Hosting & CI/CD Documentation

## Overview

This document describes the CI/CD pipeline and hosting configuration for the Loci Connect API server.

## Server Stack

- **Language:** Go 1.23+
- **Framework:** ConnectRPC (gRPC-compatible)
- **Database:** PostgreSQL with TimescaleDB
- **Hosting:** Docker / Kubernetes (recommended)

## GitHub Actions Workflows

### CI Pipeline (`.github/workflows/ci.yml`)

```yaml
name: CI

on:
  push:
    branches: [main, staging, develop]
  pull_request:
    branches: [main, staging]

jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
      - name: golangci-lint
        uses: golangci/golangci-lint-action@v6

  test:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: timescale/timescaledb:latest-pg16
        env:
          POSTGRES_PASSWORD: postgres
          POSTGRES_DB: loci_test
        ports:
          - 5432:5432
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
      - run: go test -v -race -coverprofile=coverage.out ./...

  build:
    runs-on: ubuntu-latest
    needs: [lint, test]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
      - run: go build -o bin/api ./cmd/api
```

### Deploy Staging (`.github/workflows/deploy-staging.yml`)

```yaml
name: Deploy Staging

on:
  push:
    branches: [staging]

jobs:
  deploy:
    runs-on: ubuntu-latest
    environment: staging
    steps:
      - uses: actions/checkout@v4
      - name: Build Docker image
        run: docker build -t loci-api:staging .
      - name: Deploy to staging
        # Add your deployment steps (Railway, Fly.io, AWS, etc.)
```

### Deploy Production (`.github/workflows/deploy-production.yml`)

```yaml
name: Deploy Production

on:
  push:
    branches: [main]

jobs:
  deploy:
    runs-on: ubuntu-latest
    needs: [test, lint]
    environment: production
    steps:
      - uses: actions/checkout@v4
      - name: Build Docker image
        run: docker build -t loci-api:latest .
      - name: Deploy to production
        # Add your deployment steps
```

## Environment Variables

| Variable | Description | Required |
|----------|-------------|----------|
| `DATABASE_URL` | PostgreSQL connection string | ✅ |
| `JWT_SECRET` | JWT signing secret | ✅ |
| `GOOGLE_CLIENT_ID` | Google OAuth client ID | ✅ |
| `GOOGLE_CLIENT_SECRET` | Google OAuth secret | ✅ |
| `BASE_URL` | Public API URL | ✅ |
| `SMTP_HOST` | Email SMTP host | ✅ |
| `SMTP_PORT` | Email SMTP port | ✅ |
| `SMTP_USERNAME` | Email SMTP username | ✅ |
| `SMTP_PASSWORD` | Email SMTP password | ✅ |
| `TWILIO_ACCOUNT_SID` | Twilio for SMS (optional) | ❌ |
| `TWILIO_AUTH_TOKEN` | Twilio auth token | ❌ |
| `STRIPE_SECRET_KEY` | Stripe payments | ❌ |

## GitHub Secrets Required

```
DATABASE_URL           - PostgreSQL connection string
JWT_SECRET             - Secret for JWT signing
GOOGLE_CLIENT_ID       - OAuth client ID
GOOGLE_CLIENT_SECRET   - OAuth client secret
DOCKER_USERNAME        - Docker Hub username
DOCKER_PASSWORD        - Docker Hub password
```

## Branch Strategy

```
main (production) ← PR ← staging (beta) ← PR ← develop/feature
```

### Branch Protection Rules

**Main branch:**
- Require pull request reviews
- Require status checks: `lint`, `test`, `build`
- Require conversation resolution
- Require linear history

**Staging branch:**
- Require status checks: `lint`, `test`

## Docker Configuration

### Dockerfile

```dockerfile
FROM golang:1.25.12-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /api ./cmd/api

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /api .
EXPOSE 8080
CMD ["./api"]
```

### docker-compose.yml (Development)

```yaml
version: '3.8'
services:
  api:
    build: .
    ports:
      - "8080:8080"
    environment:
      - DATABASE_URL=postgres://postgres:postgres@db:5432/loci?sslmode=disable
    depends_on:
      - db

  db:
    image: timescale/timescaledb:latest-pg16
    environment:
      POSTGRES_PASSWORD: postgres
      POSTGRES_DB: loci
    ports:
      - "5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data

volumes:
  pgdata:
```

## Deployment Targets

| Environment | URL | Branch |
|-------------|-----|--------|
| Production | `https://api.loci.dev` | `main` |
| Staging | `https://api-staging.loci.dev` | `staging` |

## Recommended Hosting Providers

1. **Railway** - Simple, Git-based deployments
2. **Fly.io** - Edge deployment with auto-scaling
3. **Render** - Easy Docker deployments
4. **AWS ECS/EKS** - Enterprise-grade
5. **Google Cloud Run** - Serverless containers

## Health Check Endpoints

- `GET /health` - Basic health check
- `GET /health/ready` - Readiness probe
- `GET /health/live` - Liveness probe
