# Tech Debt Audit — loci-connect-server
Generated: 2026-04-27

## Executive summary (ranked by impact)

- **H1**: Unbuffered `eventCh` in `StartChat` creates goroutine coordination hazard → buffered channel needed.
- **H2**: `wg.Wait()` without timeout can block forever if a goroutine panics before `Done()` → add timeout + recover in all spawned goroutines.
- **H3**: Panic in `MustGetClaimsFromContext` (auth interceptor) can crash the process if misused → replace with error return or harden preconditions.
- **M1**: Duplicate error logging across service layer obscures root cause — adopt single-layer logging with wrapped errors.
- **M2**: God files: `chat_service.go` (108KB), `poi_repository.go` (96KB) → extract logical subdomains into separate files.
- **M3**: Dead code detected by `staticcheck` (unused helpers in favorites, recents, subscription) → remove.
- **L1**: `sendEvent` allocates new `time.After` timer per retry → reuse or use `time.WithTimeout` to reduce GC pressure.
- **L2**: Hardcoded JWT secret default `"changeme"` in config → must validate at startup and fail fast (already present but worth highlighting).
- **L3**: Some `context.Background()` calls in domain services appear to initialize clients (acceptable) and bypass cancellation during SSE delivery (intentional but documented inline only).
- **L4**: `log.Fatalf` on embedding client init terminates process on misconfiguration — consider graceful degradation (503 with cached responses).

## Architectural mental model

Connect RPC server. Clean layers:

- `cmd/server/main.go` — lifecycle, signal handling, HTTP server start
- `cmd/api/router.go` — Connect route registration + utility routes (`/health`, `/metrics`, `/webhooks/stripe`)
- `cmd/api/dependencies.go` — DI container: DB → repositories → services → handlers
- `pkg/db` — pgxpool wrapper + migrations (goose)
- `pkg/interceptors` — Connect middleware chain: request ID, tracing, validation, rate limit, auth, recovery, logging, metrics
- `internal/domain/<svc>/` — per-service: `repository.go`, `service.go`, `handler.go` (sometimes `/handler`, `/service` subdirs)
- `gen/` — Connect + protobuf generated code (vendored)

Data flow: HTTP request → Connect handler → service method → repository → DB. Streaming endpoints (chat SSE) run long-lived goroutines that orchestrate LLM calls, embed-coordinating parallelism, and emit events via a channel back to the client.

Observability: OpenTelemetry tracing, structured JSON logs, Prometheus metrics.

## Findings table

| ID | Category | File:Line | Severity | Effort | Description | Recommendation |
|----|----------|-----------|----------|--------|-------------|----------------|
| F001 | Error handling | internal/domain/chat/service/chat_service.go: 1390, 1395, 1411, 1432, 1589, 1977, 1984, 1991, 1997 | M | S | Many methods log error then `return err` bare. Higher layers also log → duplicate entries, harder to trace. | Choose single logging layer. Either log with full context and return without logging callee, or return wrapped error and let top-level handler log once. |
| F002 | Error handling | internal/domain/auth/service/auth_service.go: 190, 254, 259, 263, 280, 286 | M | S | Same pattern: bare `return err` after logging in caller. | Same as F001. |
| F003 | Error handling | internal/domain/auth/repository/auth_repository.go: 141 | L | S | `UpdateLastLogin` returns raw DB err without wrapping. | Wrap: `fmt.Errorf("update last login: %w", err)` |
| F004 | Security | internal/domain/poi/poi_service.go: 87 | M | S | `ctx := context.Background()` used to init AI client; acceptable but consider deriving from request ctx to inherit deadlines/trace. | Document intentionality inline. If no request ctx available, keep as-is. |
| F005 | Security | pkg/interceptors/auth.go:179 | M | S | `MustGetClaimsFromContext` panics on missing claims. If ever called outside auth-guarded path, crashes process. | Replace with `( Claims, error )` return; callers handle error. |
| F006 | Observability | internal/domain/chat/service/chat_service.go:158 | L | S | `log.Fatalf` on embedding client init kills process on misconfiguration. Could degrade gracefully (503 with cached/static responses) and let orchestrator restart. | Consider `logger.Error` + return error → let server start with degraded mode; documented fallback behavior. |
| F007 | Performance | internal/domain/chat/service/chat_service.go:1256 | H | M | `eventCh := make(chan StreamEvent)` unbuffered. Producer goroutine may send first event before consumer starts range → blocked send, goroutine leak. `sendEvent`'s 2s timeout unblocks but drops event. | Change to `make(chan StreamEvent, 5)` (or 10). Ensure buffer large enough for burst without blocking. |
| F008 | Performance | internal/domain/chat/service/chat_service.go:1198 | M | M | `sendEvent` uses `time.After(2 * time.Second)` per retry attempt. High event rate → many timers → GC pressure. | Replace with `select { case <-time.After(...): ... }` reused, or use `time.WithTimeout(ctx, 2*time.Second)` once per send. |
| F009 | Concurrency | internal/domain/chat/service/chat_service.go:773, 2212; internal/domain/poi/poi_service.go:948, 1397, 1498, 1599, 1700 | H | M | `wg.Wait()` without timeout. If any goroutine panics without `Done()`, wait blocks forever → streaming deadlock, service hang. | Wrap each goroutine with `defer wg.Done()` and `recover` to log + call Done. Consider `select { case <-time.After(shutdownTimeout): ... }` around Wait. |
| F010 | Performance | internal/domain/chat/service/chat_service.go:108KB (god class) | M | L | Single file too large → hard to navigate, test in isolation, review. Extract: streaming orchestration, embedding, POI generation, itinerary building into separate helper files. | Split by functional domain; keep service struct thin. |
| F011 | Performance | internal/domain/poi/poi_repository.go:95KB (god class) | M | L | Same — repository handles POI, hotels, restaurants, LLM interactions. Split by aggregate (POIRepo, HotelRepo, RestaurantRepo, LLMInteractionRepo). | Create specialized repositories; compose in service layer. |
| F012 | Dead code | internal/domain/favorites/repository.go:37,49 | L | S | Unused helpers `getTableName`, `getItemColumn`. | Delete. |
| F013 | Dead code | internal/domain/recents/recents_repository.go:152 | L | S | Unused type `countRow`. | Delete. |
| F014 | Dead code | internal/domain/subscription/service.go:85 | L | S | Unused `getPlatformFeePercent`. | Delete. |
| F015 | Error hygiene | pkg/db/postgres.go:66 | L | S | `pool.Close()` error ignored during startup failure. Harmless on exit but should check/close in defer with error logged. | `if closeErr := pool.Close(); closeErr != nil { logger.Warn("close pool", "error", closeErr) }` |
| F016 | Config safety | pkg/config/config.go:79 | L | S | JWT secret default `"changeme"` is insecure if used in prod. Config validator should reject default in non-dev envs. | Add `if cfg.Auth.JWTSecret == "changeme" { return nil, errors.New("JWT_SECRET must be set") }` |
| F017 | Resource hygiene | internal/domain/chat/repository/chat_repository.go:338 | M | S | `br.Close()` error ignored after batch insert error. Should log but not fail transaction. | `if closeErr := br.Close(); closeErr != nil { logger.Warn("batch close error", "error", closeErr) }` |
| F018 | Concurrency | StartChat spawns goroutine without passing request context (chat_service.go:1266). Goroutine lives past request cancellation; it manages its own cancellation via `SendEvent` context checks. Acceptable if documented. | M | S | Cross-goroutine context propagation missing. Document that `StartChat` detaches LLM work from request lifecycle intentionally. | Add comment: "goroutine detaches from request ctx; shutdown managed by sendEvent timeouts and session TTL." |

## Top 5 (fix these first)

1. **F007 — buffer `eventCh`** (`chat_service.go:1256`)
   - Change: `eventCh := make(chan locitypes.StreamEvent)` → `make(chan locitypes.StreamEvent, 5)`
   - Impact: eliminates blocking send risk on first event; prevents goroutine leak if consumer not yet reading.
   - Test: streaming endpoint response time still < 100ms p95 under load.

2. **F009 — `wg.Wait()` safety** (multiple files)
   - Wrap every `go func(){ defer wg.Done(); ... }()` with recover; optionally add timeout around `wg.Wait()` in callers.
   - Example patch in `poi_service.go` batch functions:
     ```go
     wg := &sync.WaitGroup{}
     for ... { wg.Add(1); go func(){ defer wg.Done(); defer func(){ if r:=recover(); r!=nil { logger.Error("panic in worker", "panic", r) } }(); ... }() }
     select { case <-time.After(30*time.Second): logger.Error("wg timeout"); return errors.New("timeout") }; wg.Wait() ```
   - Impact: prevents permanent block if worker panics.

3. **F001/F002 — consolidate error logging** (all services)
   - Pick a policy: **log at the point where error becomes user-visible** (handler-level) and return wrapped errors downwards.
   - Remove `logger.ErrorContext` from inner service/repo methods; ensure they `return fmt.Errorf("sub-op: %w", err)`.
   - Add one top-level error logger in Connect interceptor or handler wrapper.
   - Effort: M. Do file-by-file.

4. **F005 — remove panic from auth** (`pkg/interceptors/auth.go:179`)
   - Rename `MustGetClaimsFromContext` to `GetClaimsFromContext` and return error like `GetClaimsFromContext` already does. Replace all `Must...` calls with proper `if claims, err := ...; err != nil { return err }`.
   - If no callers exist (likely), delete `MustGetClaimsFromContext` entirely.
   - Impact: eliminates crash vector.

5. **F012–F014 — delete dead code** (favorites, recents, subscription)
   - Remove unused functions/types identified by `staticcheck`.
   - Impact: cleaner codebase, no behavior change.

## Quick wins (low effort, medium+ severity)

- [ ] F016: validate JWT secret not default at boot (1 line)
- [ ] F015: check `pool.Close()` error on startup failure (2 lines)
- [ ] F017: check `br.Close()` error in batch failure paths (3 locations)
- [ ] F010/F011: begin god-file reduction — extract one file (e.g., `chat_embedding.go`) as pilot
- [ ] Add `go vet` + `staticcheck` to CI if not present (Makefile or GitHub Actions)

## Things that look bad but are actually fine

- **Recovering from send-to-closed-channel panic** (`chat_service.go:1216–1221`). The channel is closed by `ProcessUnifiedChatMessageStream` exactly once via `sync.Once`, but a race could cause a second send to panic. The `recover` is defensive and acceptable.
- **`any` in logging interceptor** (`pkg/interceptors/logging.go:77`). Variadic logging in slog requires `[]any`. Not a type-safety issue.
- **`context.Background()` in service initializers** (e.g., `poi_service.go:87`). These run at server boot, not per-request; using background context is fine for client creation. Documented.
- **Using `context.Background()` to deliver final SSE events** after client disconnects (`chat_process_stream.go:507–531`). Intentional to ensure terminal events are attempted even if caller context is cancelled. Inline comments explain; acceptable.
- **`wg.Wait()` without timeout** is idiomatic for in-process synchronisation where all goroutines are under our control. Still high-risk if a goroutine panics; recover mitigates, see F009.

## Open questions for the maintainer

1. Is `sendEvent`'s 2-second drop policy acceptable for final SSE events? Would we rather block longer or buffer to disk under backpressure?
2. Are there any current callers of `MustGetClaimsFromContext`? If yes, do they rely on panic semantics for control flow? (Search: `rg MustGetClaimsFromContext`)
3. In `StartChat`, the goroutine leaks `eventCh` if `ProcessUnifiedChatMessageStream` returns before the main goroutine enters `range`. The buffer mitigates but does not completely eliminate the leak. Should we add a `sync.Once` close guard or a `select` on `ctx.Done()` to close early on cancellation?
4. `log.Fatalf` on embedding init — is the intent truly fail-fast? Would users prefer degraded responses (e.g., "embeddings temporarily unavailable, showing general results") over a 503?
5. God files: do we want to split by subdomain (POI, Chat, Itinerary) now or defer until after next release? Churn on these files is high; splitting mid-flight risks merge conflicts.

---

## Re-audit 2026-09-03

Scope: modern-Go and performance pass over `main` at `2b8eb7e` (Go 1.27, connect-go 1.20, pgx v5, golangci-lint v2 + gofumpt in pre-commit). Read against the 2026-04-27 findings above.

### Closed since 2026-04-27

| Finding | Status |
|---|---|
| F005 `MustGetClaimsFromContext` | **Removed.** `pkg/interceptors/auth.go` has no panic-on-missing-claims helper. |
| F007 unbuffered `eventCh` | **Fixed.** Every stream channel is `make(chan StreamEvent, 100)`. |
| F009 `wg.Wait()` without recover | **Fixed.** `pkg/concurrency/safego.go` (`Go`/`Run`) and `errgroup` workers recover; `pkg/concurrency/llmsem.go` bounds outbound LLM calls (`AI_MAX_CONCURRENT_CALLS`, default 10). |
| F010 chat god file | **Fixed.** Split into `chat_stream_session.go`, `chat_process_stream.go`, `chat_poi_generation.go`, `chat_sessions.go`, …; largest ≈33 KB. |
| F015 `pool.Close()` error | **Invalid finding.** `pgxpool.Pool.Close()` returns nothing; there is no error to check. |
| F016 default JWT secret | **Mitigated.** Separate access/refresh secrets with a boot warning if they collide (`cmd/api/dependencies.go`). |
| — | New since April: provider fallback chain with cooldown + stream-boundary failover (`pkg/ai/fallback.go`), protovalidate interceptor, three-layer rate limiting, OTel + Prometheus + slog, 127 `_test.go` files, testcontainers integration + e2e harness. |

### Still open

| Finding | Status |
|---|---|
| F001/F002 log-then-return duplication | Open, low impact. Pick one logging layer when touching those files. |
| F011 `poi_repository.go` | Still ≈2.9 k lines. Split by aggregate when the next POI feature lands, not before. |
| F017 `br.Close()` unchecked | Open in `internal/domain/chat/repository/chat_repository.go`. |
| TODO/FIXME | 11 markers in non-test `internal`/`pkg`/`cmd`. Triage into issues or delete. |

### New findings (ranked) — with what shipped today

| # | Sev | Where | Finding | Action |
|---|---|---|---|---|
| R1 | **High** | `internal/domain/chat/handler/chat_handler.go:213`, `internal/domain/chat/service/chat_sessions.go:103,138`, `chat_process_stream.go:612` | Outer streaming goroutines had no `recover()`. A panic outside the inner `errgroup` workers killed the process for every connected client. | **Fixed** (`fb4c4b5`). Handler recovers, logs, emits a terminal error event, then closes; service sites use `concurrency.Run`. Regression test `stream_panic_test.go` drives StreamChat over a real Connect client against a panicking service. |
| R2 | Med | `internal/domain/trip/repository.go` `SaveTrip` | One `INSERT … RETURNING` round trip per day, per stop, per leg inside a single tx — latency and lock time linear in trip size. | **Fixed** (`716e3ab`). Three `pgx.Batch` sends (`insertDays`, `insertLegs`); ids assigned back in place. Covered by `multicity_integration_test.go`. |
| R3 | Med | `internal/domain/recommendation/handler.go` `recordTravelHistory` | One `SELECT` per confirmed stop. | **Fixed** (`2b8eb7e`). Single `WHERE p.id::text = ANY($1)` lookup, candidates walked in original order. New `travelhistory_integration_test.go`. |
| R4 | Med | `Dockerfile` | Runtime stage ran as root in `/root`. | **Fixed.** `loci` system user, `WORKDIR /app`, `USER loci`. Verified `id` → non-root in the built image. |
| R5 | Low | `cmd/server/main.go` | No explicit `ReadHeaderTimeout`; pprof server had no timeouts at all. Main server was already bounded by `ReadTimeout=30s`, so this is hardening, not a hole. | **Fixed.** `ReadHeaderTimeout: 10s` on both; pprof gets `ReadTimeout`/`IdleTimeout`; `WriteTimeout: 0` kept on both (streaming). |
| R6 | Med | `cmd/api/router.go:141-154`, `internal/mcp` | MCP HTTP endpoint authenticates by API key outside the Connect interceptor chain. Not verified here that it enforces the same protovalidate + per-IP/user rate limits. | **Open.** Verify parity or route MCP through the same limiter; add an e2e test hitting MCP unauthenticated + over-limit. |
| R7 | Low | `internal/domain/chat/service/chat_process_stream.go:612` | `context.WithoutCancel` + 5 min timeout for background POI persistence is correct, but the goroutine is untracked at shutdown; `srv.Shutdown` (30 s) will not wait for it. | Open. Acceptable loss (idempotent cache write); document or track with a WaitGroup on the service. |
| R8 | Low | `pkg/db/migrations` run at boot (`cmd/api/dependencies.go:177-198`) | Fine for one replica; two replicas racing goose at startup is undefined. | Open. When hosted, run migrations as an ArgoCD PreSync Job (see `PRODUCTION-READINESS.md` §3) and gate boot on schema version only. |

### Things that look bad but are fine

- `WriteTimeout: 0` on the API server — deliberate for SSE/LLM streams; application deadlines (`CHAT_STREAM_MAX_TIMEOUT_SEC`) bound them.
- `context.Background()` for AI client init (F004) — still fine.
- Prometheus label cardinality — bounded to procedure/code/plan.

### Verification run for this section

```
go build ./... && go vet ./...
go test ./internal/domain/chat/... ./internal/domain/trip/ ./internal/domain/recommendation/ ./cmd/...
go test -tags=integration -p 1 -count=1 ./internal/domain/trip/ ./internal/domain/recommendation/
docker build -t loci-api:audit . && docker run --rm loci-api:audit id
```
