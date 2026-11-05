# Go Engineering Audit — loci-connect-server (+ llm-sdk notes)

- **Revision:** `8b7f3ec30d0adc703cf2beb853b570ce8847dbd7` (main, clean worktree). llm-sdk at `f54e2b4`.
- **Date:** 2026-11-03. **Go:** 1.27.3. **Mode:** read-only static audit; no source changed.
- **Deployment facts (infra repo):** `replicaCount: 1` (goose runs on boot); RollingUpdate `maxSurge: 1, maxUnavailable: 0` (so two pods overlap during a deploy); default 30s termination grace; memory limit 768Mi, no GOMEMLIMIT; `DB_MAX_CONNS=20`; `AI_PROVIDER=openrouter`.

## Follow-up status (session 2, 2026-11-03): read this first

**Branch:** `fix/go-audit-p1` (from `8b7f3ec`), in the main checkout. No worktree. Committed locally (`git log main..`): SEC-1 `a73cda1`; DB-1 plus the `poi_repository` half of DB-2 in `2830cf3` (the pre-commit hook re-stages whole files, so they could not be split); DB-2 chat half `ea34ecd`; CON-1 `36af713`; PAY-1 `51c0061`. The hook ran `make check-all` (vet, staticcheck, `go test -race ./...`) and it passed on each commit. **Not pushed, no PR yet.**

### Done and verified (test written first and seen failing, then fixed and passing)

| ID | Change | Test |
|---|---|---|
| SEC-1 | `chat/service/chat_sessions.go` `ContinueChat`: checks ownership with `GetChatSession(ctx, userID, sessionID)` before streaming. Both callers (handler, chatbridge) pass the real owner. The stream path already checked ownership (`chat_process_stream.go:139-141`). | `chat/service/chat_continue_ownership_test.go` (unit) |
| DB-1 | `poi/poi_repository.go` `SaveLlmPoisToDatabase`: `br.Close()` before `Commit`. Before the fix, the integration test failed with `failed to commit transaction: conn busy`. The deferred rollback now ignores `pgx.ErrTxClosed`, so it no longer logs an error after every successful commit. Other `SendBatch` sites run on the pool with no commit afterwards, so they are fine. | `poi/poi_save_llm_integration_test.go` (integration) |
| DB-2 | `chat/repository/chat_repository.go` `GetOrCreatePOI` and `poi/poi_repository.go` `SavePOItoPointsOfInterest`: the lookup now uses `city_id = $2 AND lower(btrim(name)) = lower(btrim($1))`, matching `uniq_poi_city_name`. The insert adds `ON CONFLICT (city_id, lower(btrim(name))) DO UPDATE SET name = points_of_interest.name RETURNING id`. Before the fix, the tests failed with 23505. | `chat/repository/get_or_create_poi_integration_test.go`, `TestSavePOItoPointsOfInterest_MatchesTheUniqueIndex` in `poi/poi_save_location_integration_test.go` |
| CON-1 | `chat/service/chat_multicity.go`: a new `runCityRecovered` turns a city panic into that city's error, so the stop is reported and the trip continues. The per-city goroutine now uses `concurrency.Go`. The event forwarder recovers and keeps draining. Before the fix, the test crashed the test binary. | `chat/service/chat_multicity_panic_test.go` (passes with `-race`) |

Tests run after these four: `go test -count=1 ./internal/domain/chat/... ./internal/domain/messaging/... ./internal/domain/subscription/...` passes. `go test -race ./internal/domain/chat/service/` passes. Integration tests passed only for the targeted cases: `-tags=integration -run 'MatchesTheUniqueIndex|TestSavePOItoPointsOfInterest|TestSaveLlmPois' ./internal/domain/poi/ ./internal/domain/chat/repository/`.

### Unit-verified, not integration-tested

**PAY-1** (Stripe event lost on retry):
- Written to disk:
  - `ForgetWebhookEvent` added to the `payment.Repository` interface and implemented in `payment/repository.go` (`DELETE FROM webhook_events WHERE event_id=$1`).
  - `payment/service.go`: `processStripeEvent` now records the event id, calls the new `applyStripeEvent` (the old switch), and on error calls `ForgetWebhookEvent` with `context.WithTimeout(context.WithoutCancel(ctx), 5s)`.
  - Test `TestProcessStripeEvent_FailedEventIsRetriedNotSkipped`, plus a fake `ForgetWebhookEvent`, in `payment/service_test.go`. The test failed (red) before the implementation.
- Verified later in session 2: `go build ./...` passes and `go test ./internal/domain/payment/ ./internal/domain/bundle/` passes, including the new test. It is committed in its own commit, which can be reverted on its own if the owner prefers the transactional approach.
- Not yet covered by an integration test: the real `DELETE FROM webhook_events` against Postgres.
- Why it is safe to re-run a failed event: `bundle.Service.Fulfil` is documented as idempotent per checkout session, and the other branches are upserts or a customer link. The stronger alternative is to record the event id in the same transaction as the side effects.

### Session 3 (2026-11-04): remaining P1 work, not committed

These changes are in the working tree only, on top of `09b6778`. The owner has not yet asked for them to be committed.

| ID | Change | Test |
|---|---|---|
| HTTP-1 | `cmd/api/router.go`: `connect.WithReadMaxBytes` of 1 MiB on every Connect service, 14 MiB on Speech. | `cmd/api/read_limit_test.go`, against the real `SetupRouter`. Covers an oversized request, a small gzip body that inflates past the limit, and Speech at both sides of its limit. |
| HTTP-2 | `statistics/handler.go`: streams share one cached read (30s TTL, singleflight, query detached from the caller, 10s timeout, failures not cached). 30s minimum interval, 10-minute stream lifetime, first event sent on open, and `last_updated` now says when the numbers were read. | `statistics/handler_stream_test.go`: `synctest` for exact counts on a fake clock, plus one test over Connect. |
| PAY-1 | Integration tests for the real `DELETE FROM webhook_events`. | `TestForgetWebhookEvent_*` in `payment/repository_test.go`. Making the delete a no-op fails the test. |

**Differences from the original proposals:**
- **Speech limit is 14 MiB, not about 11 MiB.** The web and iOS clients both send JSON, where base64 turns the 10 MiB the proto allows into about 13.4 MiB. An 11 MiB limit would have refused valid recordings, and the "13 MiB is refused" test proposed above would have enforced that. An 11 MiB recording in JSON is now the refused case.
- **Streams send an event on open.** With a lifetime limit, a client asking for a long interval would otherwise reconnect forever and never receive anything.
- **`singleflight.Do`, not `DoChan`.** `DoChan` re-panics on its own goroutine, where no recover can reach it. A stream leaving mid-query therefore waits for that query, at most 10s.

**Still open from these two findings:**
- `/mcp` is outside the Connect limit, and `internal/mcp` has no body limit of its own. Stripe and Telegram already have theirs. Whether the MCP SDK limits the body was not checked.
- Concurrent stats streams have no cap: the query cost is now flat, but each stream still holds a connection and a goroutine. Whether the IP rate limiter counts stream opens was not checked.
- The unary `GetMainPageStatistics` is also public and still queries on every call. It was left uncached because it was outside this finding.

**Full runs (2026-11-04), on the code above with API keys and tokens removed from the environment:**

| Suite | Command | Result |
|---|---|---|
| Unit | `go test -race -count=1 ./...` | exit 0, 93 packages ok, no data races reported |
| Lint | `golangci-lint run ./...` | 0 issues |
| E2E | `go test -tags=e2e -p 1 -count=1 ./cmd/api/...` | ok |
| Integration | `go test -tags=integration -p 1 -count=1 ./...` | **exit 1:** 95 packages ok, `internal/domain/achievements` ended with `signal: terminated` at 10.9s and no failed assertion |
| Integration, that package again | `go test -tags=integration -count=1 ./internal/domain/achievements/`, twice | ok both times |

The cause of the `signal: terminated` was not found, and the branch does not touch that package. If it comes back, look for what is signalling the test binary before looking at the tests.

### Session 4 (2026-11-04): PR merged, plain EXPLAIN on production

PR #154 is merged to `main` as `be63a60`, with all nine checks green. **It is live** through infra PR #544 (`8cb72b8`), promoted by hand because the CD promote step failed (see below). Only the API and the rerank CronJob moved, so Postgres did not restart.

**Checked on production after the rollout:** `loci-api` runs `be63a60` with 0 restarts, and `loci-api-production` is Synced and Healthy in ArgoCD. `/health` and `/ready` return 200, and the pod had logged 0 errors a few minutes in. A 2 MiB login got `resource_exhausted` ("message size 2097164 is larger than configured max 1048576"). Over `curl -N`, a stats stream sent its first event at 0.43s and its next at 30.46s. The SEC-1, PAY-1, DB-1, DB-2 and CON-1 fixes were not exercised on production; they rest on their tests.

**Method.** Plain `EXPLAIN` on `loci_production` through `psql` in `loci-postgres-0`, inside `BEGIN READ ONLY` with a 5s `statement_timeout`, then rolled back. Parameters were invented, except the most common `city_id`, which came from `pg_stats`. Row counts, index definitions and planner statistics were also read. The queries were run with `enable_seqscan=off` as well, to tell "the table is small" from "the index cannot be used". No table rows were read and no user value was printed. These are planner estimates, so **no timings were measured**.

**Scale at the time:** `points_of_interest` 946 rows, `llm_interactions` 113, `restaurant_details` 41, `cities` 20, `hotel_details` 10. These are `pg_stat_user_tables` estimates.

| ID | Result | Evidence | Next |
|---|---|---|---|
| DB-6 | **CONFIRMED** | Queries using `location::geography` seq scan even with `enable_seqscan=off`, on `points_of_interest` (including `SearchPOIsHybrid`), `hotel_details`, `restaurant_details` and `cities`. The GIST indexes are on the geometry, so they cannot serve these predicates. When `city_id` is given, the city index narrows the scan first. | Cheap fix, little to gain at 946 rows. Add it when a table reaches about 10k rows or the query shows up in a profile. |
| DB-5 | Plan is fine; the code issue stands | `GetPOIsByCityID` for the most common city uses `idx_poi_city_id`, about 102 rows. `SearchPOIsHybrid` sorts every match with no LIMIT. | The missing ORDER BY, which makes the "top 8" arbitrary, is a correctness bug and does not depend on scale. Fix it with the P2 work. |
| DB-8 | **CONFIRMED** in shape | The plan is Limit, Sort, GroupAggregate, Index Scan, so every interaction is aggregated before the LIMIT. | No cost at 113 rows across all users. Defer. |
| DB-10 | Does not apply today | For the most common city the planner uses a bitmap scan and an exact sort, not HNSW. HNSW appears only with `enable_sort=off`, so results cannot come back short on this plan. pgvector is 0.8.2, so `hnsw.iterative_scan` is available if that changes. | Recheck if a city grows large. |

**New finding, DB-12:** planner statistics put 55% of `points_of_interest.embedding` values at NULL. `FindSimilarPOIsByCity` filters those rows out, so about half the POIs cannot be found by semantic search in that path. Why they are NULL was not investigated.

Not settled by these plans: DB-7 (a concurrency issue, not a plan), CACHE-2, OPS-8 and MEM-1 (these need metrics).

**CD failure, new finding OPS-9:** `_deploy.yml` builds the images and pushes `promote/loci-<sha>` to the infra repo, then `gh pr create` fails with `GraphQL: Resource not accessible by personal access token (repository.pullRequests)`. Both `3e734b9` and `be63a60` failed this way. `INFRA_TOKEN` needs pull request access on `LuminaVault/LuminaVaultInfra`. The branch `promote/loci-be63a60` exists, is one commit ahead of infra `main` with nothing behind, and has no PR.

**New finding, HTTP-6:** `StreamMainPageStatisticsRequest` documents "Default: 30 seconds", but the proto rule `gte: 1` rejects an unset interval with `invalid_argument`, so the default cannot be reached. Either make the field optional or drop the comment.

**Kubernetes API:** requests to `lumina-green:6443` timed out for a few minutes during this session while the production API kept answering 200. It recovered without intervention. The cause was not investigated.

### Session 5 (2026-11-04): PAY-2, on branch `fix/go-audit-pay2`, not deployed

**Production Stripe secrets, new finding PAY-3 (checked by shape only, no value printed):**
- `STRIPE_WEBHOOK_SECRET` starts with `whsec_` but **ends in a space**, and config did not trim it. Every real Stripe signature would fail against that key, so every delivery would get a 400.
- `STRIPE_API_KEY` is 27 characters plus a trailing space and has **no `sk_` or `rk_` prefix**, so it is not a standard Stripe secret key. Checkout has probably never worked in production. No call to Stripe was made to confirm it.
- `webhook_events` has 0 rows and `subscriptions` has 0 rows. That fits both problems, and also fits no Stripe traffic at all. Whether an endpoint is registered in the Stripe dashboard was not checked.

Trimming fixes the webhook secret on the next deploy, if the value under the space is right. **The API key needs resealing by the owner.** Its format is deliberately not a boot check, because that would take the whole API down on the next deploy.

| ID | Change | Test |
|---|---|---|
| PAY-2b | Stripe values are trimmed in `config.go`. Production refuses to boot with an API key and no webhook secret. `WebhookHandler` answers 503 to everything when the secret is empty, in any environment. Before the fix, a signature made with the empty key got 200 and the event was processed. | `payment/webhook_test.go`, `TestLoad_TrimsStripeValues`, `TestLoad_ProductionWithStripeNeedsWebhookSecret`, `TestLoad_ProductionBootsWithUnusualStripeKey` |
| PAY-2a | Migration `0131` adds `subscriptions.last_stripe_event_at`. `UpsertSubscription` skips an event older than the stored one and returns `ErrStaleSubscriptionEvent`, which the service logs and answers as success. Same-second events apply in arrival order, because Stripe's timestamps are whole seconds. | `TestUpsertSubscription_*` (integration), `TestProcessStripeEvent_RecordsWhenTheEventWasCreated`, `TestProcessStripeEvent_StaleSubscriptionEventIsNotAnError` |

**Not covered by PAY-2a:** the row is per user, not per subscription. A late `deleted` for an old subscription, created after a new subscription's event, would still overwrite the new one.

**SEC-1, `AND user_id` on `UpdateSession` dropped.** The original proposal does nothing: every caller loads the session by id and passes it back, so the filter compares the row with itself. The useful check is that each entry point compares the owner with the caller. `ContinueChat`, `GetSessionPOIs`, `EndSession`, the stream path, `tripaction.ownedSession` and `watch.Create` all do. `watch.Run` compares with the watch's own user. No sibling of SEC-1 was found. This is not the full security review the finding asked for.

**DB-1 log search:** no `Failed to commit transaction for saving LLM POIs` in the pod started at 14:40Z on 2026-11-04. Older logs are gone and nothing shows that path has run since, so this neither confirms nor refutes the fix on production.

**Local disk:** free space on the machine swung between 706 MiB and 54 GiB during this work, with other sessions running `xcodebuild` and `go test` at the same time. A full-repo pre-commit race build needs several GiB. If it fails with `no space left on device`, retry when there is room. The first doc commit failure was stale `vendor/`, not disk.

### Session 5, continued: OPS-1 and OPS-2, same branch, not deployed

| ID | Change | Test |
|---|---|---|
| OPS-1 | `main` only calls `run` and exits with its result, so `os.Exit` no longer skips cleanup. The signal cancels the loops and starts the HTTP drain together. A drain that times out closes the open connections and is not an error. Order after that: wait for the loops (5s at most), `deps.Cleanup`, metric flush, trace flush. HTTP drain is now 15s instead of 30s. | `TestServe_*`, `TestLoops_WaitGivesUpOnAStuckLoop` |
| OPS-2 | `cmd/server/lifecycle.go` supervises the 8 loops. A panic logs the stack and restarts the loop after 1s, doubling to at most 1 minute, and a run of 1 minute or more resets the delay. An error stops the loop, as before, but is now counted. A nil return (switched off) and an error during shutdown are not counted. New metrics: `loci_background_loop_up{loop}` and `loci_background_loop_stops_total{loop,reason}`. `concurrency.Go` and `Run` also log the stack. | `TestLoops_*`, `TestGo_PanicLogsTheStack`, `TestRun_PanicLogsTheStack` |

**Restart safety, checked by reading each loop:** the runners keep state in locals, the advisory lock unlock is deferred so a panic releases it, and the Telegram cursor is in the database.

**Real process check** (binary with fake keys against a throwaway Postgres, SIGTERM):

| Case | Exit | Time | Log |
|---|---|---|---|
| Idle | 0 | 0.0s | `server stopped gracefully`, `cleanup completed` |
| Login request held open with a partial body | 0 | 15.0s | `drain did not finish`, `cleanup completed` |

The old code answered the second case with `graceful shutdown failed` and exit 1, with no cleanup. That comparison is from reading the old code; the old binary was not run.

**Different from the proposal above:**
- `terminationGracePeriodSeconds` is not set, because the chart has no value for it and this branch does not touch infra. 15s + 5s leaves 10s of the default 30s for cleanup. Streams now get 15s to finish, not 30s. To give them longer, raise both together in infra.
- Restart is per loop, not per tick. A panic in one tick loses that loop's ticker position and nothing else.
- `loci_background_last_success_timestamp` and `..._tick_total` were not added, so **a loop that hangs without panicking still shows as up**. No alert rule exists yet for the new metrics.
- Alert on `loci_background_loop_stops_total`, not on `loci_background_loop_up`: a loop switched off by configuration reads 0.

**Not changed:** goroutines detached from a request can still be running when the pool closes. There is no `preStop` delay, so a request that arrives between SIGTERM and endpoint removal can be refused, as before.

### Still to do

1. **PAY-3:** reseal `STRIPE_API_KEY` with a real key, and the webhook secret without the space. Check that an endpoint is registered in the Stripe dashboard.
2. **OPS-9:** give `INFRA_TOKEN` pull request access on the infra repo. Until then every promote needs a PR by hand. When opening one, leave out `apps/loci/data/postgres.yaml` and `bundle-forge-job.yaml` unless the database image really has to change: the generated branch moves them too, and moving the Postgres image restarts the production database. `promote/loci-be63a60` and `promote/loci-3e734b9` are leftovers from the failed runs and were not merged.
3. **PAY-1, optional:** record the event id in the same transaction as the side effects.
4. **Then the rest of P2/P3** from the roadmap below. DB-6, DB-8 and DB-10 can wait for scale. DB-5's ORDER BY and DB-12 should not.

## Commands run

| Command | Result |
|---|---|
| `GOFLAGS=-mod=vendor GOPROXY=off go vet ./...` | exit 0 |
| `GOFLAGS=-mod=vendor GOPROXY=off go test -race -count=1 ./...` (API keys unset, no build tags) | exit 0; 93 packages ok, **no data races reported** |
| Integration/e2e tests (`-tags=integration`, Docker) | **not run** |
| staticcheck, golangci-lint, pprof, EXPLAIN | **not run** (no tooling or DB access authorised) |

The race detector only covers the paths the unit tests exercise, so a clean run doesn't prove there are no races. None of the latency or row-count effects below were measured; each fix says how to measure it.

Status labels: **CONFIRMED** = a defect shown by the code (and by library source where cited). **RISK** = a risk supported by the code. **MEASURE** = a hypothesis to measure first.

---

## Executive summary

**Top fixes, in order:**

1. **ContinueChat skips the session ownership check** (P1). An authenticated user who knows another user's session UUID can write into that user's chat.
2. **Stripe webhook: an event that fails to process is lost on retry** (P1). The event ID is recorded before the side effects run, so Stripe's retry is skipped as a "duplicate".
3. **`SaveLlmPoisToDatabase` commits while its batch is still open** (P1). pgx rejects the commit, so these POIs are never saved and the connection is discarded each time. The caller only logs a warning.
4. **A panic in a raw goroutine takes down the only replica** (P1/P2). There are 26 bare `go` statements with no recover; the multi-city stream fan-out is the hottest of them.
5. **No request-size limit on Connect handlers, and the client's timeout header beats the server's deadline** (P1/P2). Both are memory and slot-exhaustion risks on a single pod.
6. **Shutdown loses work.** `os.Exit(1)` skips Cleanup, background loops are never joined, and a loop that panics stays dead with no metric showing it.

**Not inspected:** a full security audit (the Cloudflare `security-audit` skill was not run), live metrics, query plans, sealed secret values, `cmd/loci-mcp`, `cmd/bundle-forge`, and most of `export/`, `recap/render`, `placeintel`.

---

## Findings

### P1

| ID | Status | Location | Issue → Fix |
|---|---|---|---|
| SEC-1 | CONFIRMED | `internal/domain/chat/service/chat_sessions.go:141`, `chat_stream_session.go:40`, `chat_repository.go:~1584` | `ContinueChat(ctx, _, sessionID, …)` discards the authenticated userID. `GetSession` never compares it with `session.UserID`, and `UpdateSession` filters on `WHERE id=$1` only. Work then runs (and is billed) as the victim. **Fix:** return NotFound when `session.UserID != userID`, and add `AND user_id=$N` to the update. **Test:** a handler test using a foreign session ID. *(Outside the engineering scope, so it needs a security review.)* |
| PAY-1 | CONFIRMED | `internal/domain/payment/service.go:477` | `RecordWebhookEvent` inserts the event ID before the switch, outside any transaction with the side effects. If the upsert or fulfilment then fails, the handler returns 500. Stripe retries, the unique constraint rejects the ID, and the retry returns 200. Upgrades, purchases and refunds can be skipped silently. **Fix:** record the ID in the same transaction as the side effects, or delete the row when processing fails. **Test:** a repo stub that fails once; the second delivery must apply the change. |
| DB-1 | CONFIRMED (by pgx contract: `vendor/.../pgx/v5/conn.go:947` says "BatchResults must be closed before the connection is used again") | `internal/domain/poi/poi_repository.go:2941-2953` | The code calls `br := tx.SendBatch(...); defer br.Close(); …Exec…; tx.Commit(ctx)`. Commit runs while the pipeline still holds the connection lock ("conn busy"), so pgx closes the connection. Nothing is saved, and the caller only logs a warning (`poi_llm_generation.go:140`). **Fix:** call `br.Close()` and check its error before `Commit`, as `trip/repository.go:600-609` does. **Validate:** an integration test, then search logs for "Failed to commit transaction for saving LLM POIs". |
| DB-2 | CONFIRMED | `chat/repository/chat_repository.go:1968-1993` (inside the SaveInteraction tx); `poi/poi_repository.go:1911-1946` | `GetOrCreatePOI` looks up with `WHERE name=$1`, but the unique index is on `(city_id, lower(btrim(name)))` (`0068_…up.sql:68`). A name that differs only in case or whitespace, or a concurrent insert, raises 23505 and rolls back the whole interaction and itinerary. **Fix:** reuse the canonical upsert in `poi/poi_identity.go:60-80` (`ON CONFLICT … RETURNING id`). |
| CON-1 | CONFIRMED | `internal/domain/chat/service/chat_multicity.go:138`, `:198` | City goroutines are launched with a bare `go` and no recover. A panic in `runStop` (prompting, parsing or persisting) crashes the process and every live stream with it. **Fix:** `concurrency.Go(&wg, l.logger, …)`, and turn a panic into a per-stop error event. **Test:** make the `runCityFn` seam panic on stop 1. |
| HTTP-1 | CONFIRMED | `cmd/api/router.go:199-216` | There is no `connect.WithReadMaxBytes`, and Connect's default is unlimited. Proto `max_len` rules run only after the whole message is decoded into memory. **Fix:** `WithReadMaxBytes(1<<20)` globally and a larger limit for Speech (14 MiB as built, see session 3), or wrap the mux in `http.MaxBytesHandler`. |
| HTTP-2 | RISK | `internal/domain/statistics/handler.go:121-165` | `StreamMainPageStatistics` is unauthenticated. Each stream runs a heavy aggregate query on its own ticker (interval can be as short as 1s) with no cache and no cap on stream count, so N streams cost N queries per tick against 20 connections. **Fix:** one shared ticker that broadcasts to all streams (or a 30s cache behind singleflight), a server-side minimum interval, and a maximum stream lifetime. |

### P2

| ID | Status | Location | Issue → Fix |
|---|---|---|---|
| CON-2 | RISK | 26 bare `go` statements with no recover, e.g. `poi/poi_search.go:221,243`, `poi/poi_identity.go:211`, `push/{notifier.go:66,proactive.go:86,social.go:122}`, `achievements/service.go:231`, `auth/service/auth_service.go:366,583`, `linkimport/linkimport.go:204`, `localcontext/signals.go:226`, `chat/service/{chat_poi_embeddings.go:75,chat_trip_actions.go:80,chat_poi_generation.go:194}`, `messaging/telegram/webhook.go:133,145`, `pkg/ai/fallback.go:370` | `RecoveryInterceptor` only covers the handler goroutine, so any of these can kill the single replica. **Fix:** route them through `pkg/concurrency` (`Go` for joinable work, `Run` otherwise), and add a lint rule (forbidigo) banning `go func` outside `pkg/concurrency`. Also replace `context.Background()` with `context.WithTimeout(context.WithoutCancel(ctx), d)` so traces carry over and every call has a deadline. `poi_search.go:221` currently has no deadline at all. |
| CTX-1 | CONFIRMED | `chat/service/chat_process_stream.go:373` | `context.WithoutCancel(ctx)` also removes the parent's deadline, so the per-city (3 min), multi-city and stream budgets never reach the LLM workers. Each city gets a fresh 5 minutes. The comment at `chat_handler.go:159` claims otherwise. **Fix:** re-apply the parent's deadline (`if dl, ok := ctx.Deadline(); ok { WithDeadline }`), or drop `WithoutCancel`, since `llmCtx` is already detached from the client. |
| HTTP-3 | CONFIRMED | `pkg/interceptors/timeout.go:67-69` | On unary calls, `Connect-Timeout-Ms` from the client replaces the server's default (30s) and chat (3 min) deadlines with no cap. Streams are already capped by `StreamMax`. **Fix:** use `min(client, serverPolicy)`. |
| LLM-1 | CONFIRMED | `pkg/ai/fallback.go:147,412-418` → `chat_service.go:178` → `generation_plan.go:134` → `generation_key.go:405` | `chainClient.Model()` returns whichever model answered last, process-wide, and that value goes into the generation cache key. One request failing over changes the key for every concurrent request, so Haiku answers get cached under the fallback model's name. **Fix:** build the key from the configured head model (`entries[0]`) and keep `active` for telemetry only. |
| LLM-2 | RISK | `pkg/ai/byok.go:92-104`; `aicreds/routing.go:100` (`&http.Client{}`) | BYOK clients have no generate or stream timeout and skip the chain's first-chunk and idle stall checks. A silent user gateway holds a pod-wide LLM slot (10 by default) for up to 5 minutes. **Fix:** pass the configured timeouts and wrap these clients in `withStreamTimeouts`. |
| LLM-3 | RISK | `chat_stream_session.go:392`, `chat_poi_generation.go:47,245`, `chat_persistence.go:156` | These sites call the LLM without `acquireLLMSlot`, and `Acquire` has no wait limit. **Fix:** move the semaphore into one wrapper around `aiClient` with a 10–20s acquire timeout that returns a capacity error. |
| LLM-4 | RISK | `pkg/llmerrors/llmerrors.go:51-53`, `fallback.go:168-183`, per-link retries (`MaxRetries=3`) | Retries multiply: about 4 × links × 3 parts × multi-city concurrency (2). `ErrRateLimited` never benches a link, so every request walks the whole retry ladder again against a head provider that is rate-limited. **Fix:** cool a link down for about 30s (or Retry-After) on 429, and cut a link's own retries to 0–1 when a fallback follows it. |
| LLM-5 | RISK | `chat/service/chat_events.go:41-95` | After about 6s, `sendEvent` drops the event, including COMPLETE and itinerary events. A slow reader therefore gets a stream with no terminal event, and the run is recorded as incomplete. **Fix:** never drop terminal events (block on ctx); time-limited dropping stays for chunks only. |
| CACHE-1 | RISK | `chat/service/gastronomy.go:60-77` | The cache is shared per city but has no singleflight, so N concurrent misses make N LLM generations. **Fix:** `singleflight.Group` keyed on the cache key (the pattern already exists in `city/city_resolver.go:137`). |
| PAY-2 | RISK | `payment/repository.go:408-428`; `pkg/config/config.go:612` | (a) The subscription upsert overwrites unconditionally, so out-of-order Stripe events can re-activate a deleted subscription. **Fix:** store `event.Created` and update only when it is newer. (b) An empty `STRIPE_WEBHOOK_SECRET` is accepted, which makes signatures forgeable. **Fix:** fail at boot in production when it is empty. |
| LOG-1 | CONFIRMED | `localcontext/openweather.go:73,80`; `localcontext/handler.go:78` | The `appid=<key>` query string ends up in the `*url.Error` text, which is logged on timeout (together with the user's lat/lon). **Fix:** strip the query from URL errors in the adapters and httpx, or use the existing `redactErr`. |
| DB-3 | CONFIRMED (N+1) | `trip/repository.go:245-249`, `:752-800` | `ListTrips` runs about 6 queries per trip, up to about 601 round trips for 100 trips; the ICS calendar feed is about 301. **Fix:** batch loaders with `trip_id = ANY($1)`, or one aggregate query for the list view. |
| DB-4 | CONFIRMED (N+1) | `recents/recents_repository.go:283` | One query per city (up to 50). Also, the count query groups by raw `city_name` while the list groups by a COALESCE'd value, so totals can disagree. **Fix:** a single query using `LATERAL … LIMIT 5`. |
| DB-5 | CONFIRMED | `poi/poi_repository.go:2224-2271` (`SearchPOIsHybrid`); `:601-613` (`GetPOIsByCityID`) | No LIMIT: they return every POI in a radius or city, and callers slice the result in Go (with no ORDER BY in the second, so the "top 8" are arbitrary). **Fix:** add a LIMIT and a deterministic ORDER BY, plus a separate `COUNT(*)` for `score_handler.go`. |
| DB-6 | MEASURE | `poi_repository.go` ST_DWithin/`<->` on `location::geography` (≈10 sites) | The columns are geometry with GIST indexes on the geometry, and no `(location::geography)` expression index exists, so these queries probably sequential-scan. **Validate:** `EXPLAIN (ANALYZE, BUFFERS)` in staging. **Fix:** an expression GIST index (created CONCURRENTLY), or a geometry `&&` prefilter. |
| DB-7 | RISK | `chat_repository.go:1616-1624`, `chat_stream_session.go:126,297` | `conversation_history` is updated read-modify-write, so concurrent turns lose messages, and every message rewrites the whole JSONB. **Fix:** an atomic `history = history \|\| $2::jsonb` append. |
| DB-8 | MEASURE | `chat_repository.go:1029-1121` | The chat-history list runs `json_agg` over the user's entire history before LIMIT/OFFSET. **Fix:** page the session keys first, then fetch only those sessions. |
| OPS-1 | CONFIRMED | `cmd/server/main.go:186-188, 247-273` | `os.Exit(1)` after a Shutdown timeout skips `deps.Cleanup`, the analytics flush, and the trace and metric flushes. Any open stream (StreamChat, statistics) makes Shutdown hit its timeout. `backgroundCtx` is cancelled only after Shutdown, loops are never joined, and 30s Shutdown plus Cleanup is longer than the 30s grace period. **Fix:** cancel `backgroundCtx` when the signal arrives, wait for the loops (bounded, about 10s), return instead of `os.Exit`, use a 15s Shutdown, and set `terminationGracePeriodSeconds: 60`. |
| OPS-2 | CONFIRMED | `pkg/concurrency/safego.go:30-40`; runners such as `watch/runner.go:46-57` | After a panic, `Run` logs one line with no stack trace and the loop stays dead until the next deploy. Nothing reports whether the loops are healthy. **Fix:** recover per tick and log `debug.Stack()`; add metrics `loci_background_last_success_timestamp{loop}` and `…_tick_total{loop,result}` with alerts. |
| OPS-3 | CONFIRMED | `messaging/telegram/webhook.go:128-135` | The webhook returns 200 before doing the work (which can take up to 10 min), and nothing drains that work at shutdown. Every deploy loses Telegram replies in flight, and Telegram will not redeliver. **Fix:** a bounded drain on shutdown, or better, an outbox row written before the 200. |

### P3

| ID | Status | Location | Note |
|---|---|---|---|
| OPS-4 | RISK | `tripbrief/repository.go:61-69`, `runner.go:160-172` | Candidates that are never claimed stay in a fixed batch of 50 (UUID order), so users after them starve. Write a terminal row for them or page with a cursor. |
| OPS-5 | RISK | `gamification/repository.go:1003`, `journal/repository.go:239`, `recap/service.go:459` | Rows that fail forever stay at the head of fixed-order batches. Add `attempts`/`next_attempt_at` columns. |
| OPS-6 | RISK | `boards/daily.go:185` | The daily LLM post has no timeout per tick and no cap on attempts per day (up to 96 calls/day against the $20 Haiku cap). |
| OPS-7 | CONFIRMED | `chat/service/chat_service.go:344-357` | The generation sweeper (`for range ticker.C`) takes no ctx and cannot be stopped. |
| OPS-8 | MEASURE | `pkg/pglock/pglock.go:33-57` | The advisory lock holds one connection for the whole tick, and the six runners line up every 30 minutes. Export `pgxpool.Stat()` (AcquireDuration, EmptyAcquireCount) as Prometheus gauges first. |
| MEM-1 | MEASURE | infra `values-production.yaml` | 768Mi limit with no `GOMEMLIMIT`, so the GC doesn't know about the limit. Set `GOMEMLIMIT≈650MiB`, and check pod OOMKill history first. |
| CACHE-2 | MEASURE | `pkg/cachestore/store.go:67`; `poi/poi_nearby.go` keys | The in-memory cache has no size cap, and keys use `%f` lat/lon plus the user ID, so the hit rate is probably low. Round coordinates and use a size-capped LRU. A Redis hit is copied back to memory with a fixed 5-minute TTL. |
| RL-1 | RISK | `pkg/interceptors/rate_limit_helpers.go:73-78`; `internal/mcp/middleware.go:43-58` | IPv6 is keyed per /128 and the limiter evicts an arbitrary entry when full. The MCP endpoint queries the DB for the API key before any rate limit. |
| HTTP-4 | CONFIRMED | `pkg/httpx/client.go:61-66,195`; `openrouter/chat.go:160` | `MaxRetries<=0` still gives 2 retries. 2xx bodies are read with an unbounded `ReadAll`, and some bodies are closed without draining. There are 5 separate "signals" clients, so the per-host limit is not global. |
| HTTP-5 | CONFIRMED | `pkg/interceptors/recovery.go:30-33`; `cmd/api/router.go:629` | The panic value and a raw DB error are returned to clients. Return generic messages. |
| CHAT-1 | CONFIRMED | `chat_stream_session.go:745,756` + `chat_handler.go:290` | Two terminal error events are sent per failure (and replayed on resume). |
| CHAT-2 | RISK | `chat_sessions.go:110-127` | When StartChat panics, the caller (the Telegram bridge) gets an empty answer that looks successful. |
| LLM-6 | RISK | `aicreds/routing.go:146-163` | The BYOK resolve hits the DB on every LLM call with no cache, and a transient DB error routes the call to the paid chain. |
| DB-9 | CONFIRMED | `chat_repository.go:2041-2063,2085`; `favorites/repository.go:170`; `payment/repository.go:217,285` | `rows.Err()` is not checked, so a truncated read counts as success. Use `pgx.CollectRows`. |
| DB-10 | MEASURE | `poi_repository.go:2133` | HNSW search plus a city filter may return fewer rows than the limit. Check the pgvector version, then use `hnsw.iterative_scan`. |
| DB-11 | — | `chat_repository.go:2115-2146` | `GetTrendingDiscoveries` has no callers and its subquery would expose other users' messages. Delete it. |

### llm-sdk (`github.com/FACorreiaa/go-genai-sdk`, ~2.9k LOC of its own)

| ID | Status | Location | Note |
|---|---|---|---|
| SDK-1 | CONFIRMED | `lib/chat.go:96-101` | The `GenerateStream` retry does nothing: the closure always returns a nil error because stream errors only appear while iterating. Remove the wrapper or retry on the first chunk. |
| SDK-2 | RISK | `lib/retry.go:56-72` | String matching retries `"quota"`/`resource_exhausted`, which includes daily quota exhaustion (a permanent failure until reset), costing about 14s of wasted backoff per call. It also ignores Retry-After, and it stacks with the server's chain retries (LLM-4). |
| SDK-3 | RISK (needs a docs check) | `lib/embedding.go:81-89` | The config is `nil`, so no `OutputDimensionality` is sent. `gemini-embedding-001` likely defaults to more than 768 dims, while the columns are `VECTOR(768)`. This is latent while prod uses OpenRouter, but the Gemini path would break inserts. |
| SDK-4 | CONFIRMED | `lib/embedding.go:169-173` | `GenerateUserPreferenceEmbedding` iterates a map, so the same preferences produce different text and embeddings each time. Sort the keys. |
| SDK-5 | CONFIRMED | `lib/embedding.go:94` | Logs `text_preview` (user query text) at Error level, and the byte slice can split a UTF-8 character. |
| SDK-6 | MEASURE | `lib/embedding.go:195-210` | `BatchGenerateEmbeddings` makes N sequential calls even though the API accepts several contents per request. |

---

## Status of the April 2026 TECH_DEBT_AUDIT.md

These are fixed: F007 (eventCh buffered to 100), F008 (`NewTimer` + `Stop`), and F009 at the old call sites (`concurrency.Go`, errgroup recover). F009 has **regressed** in the new multi-city code (CON-1). F018 has changed shape and is now CTX-1.

## Proposals rejected or revised

- **REJECT:** a subagent reported "the test build is broken by pgxmock v5.2.0 vs pgx v5.12". With the vendored modules, `vendor/.../pgxmock/v5/batch.go:76` has `Tx()`, and the race test run compiled and passed every pgxmock package (chat/repository, auth/repository, city, list, …). That build was probably resolved against a stale module cache. **Revised in session 4:** this rejection looks wrong. `vendor/` is git-ignored, so a local vendored run can differ from a clean checkout, and `d83f006` on `main` later fixed exactly this ("build tests against pgx v5.12 via patched pgxmock"). When that commit reaches a checkout with an older `vendor/`, the pre-commit hook fails with `go: inconsistent vendoring`; run `go mod vendor`.
- **REVISE:** "make TrackSearch goroutines faster". The real issue is the missing deadline and recover (CON-2), not speed.
- **MEASURE FIRST:** all index proposals (DB-6, DB-8, DB-10), GOMEMLIMIT, cache LRU, per-chunk JSON round-trip in streaming (`chat_handler.go:716-719`): profile with pprof before changing.

## Roadmap

| Order | Work | Files | Risk | Verify |
|---|---|---|---|---|
| 1 | SEC-1 ownership check | chat_sessions.go, chat_stream_session.go, chat_repository.go | low | foreign-session handler test |
| 2 | DB-1 `br.Close()` before Commit | poi_repository.go | low | integration test; log search |
| 3 | PAY-1 transactional idempotency + PAY-2b boot check | payment/service.go, repository.go, config.go | med | fail-once retry test |
| 4 | CON-1/CON-2 recover everywhere + lint ban | listed sites, .golangci.yml | low | panic-seam tests |
| 5 | HTTP-1 ReadMaxBytes (done), HTTP-3 min(timeout) | router.go, timeout.go | low | read-limit tests; header-cap test |
| 6 | OPS-1/OPS-2 shutdown + loop health metrics | main.go, safego.go, runners, infra values | med | SIGTERM test; dashboards |
| 7 | DB-2 canonical upsert | chat_repository.go, poi_repository.go | low | case/whitespace test |
| 8 | CTX-1, LLM-1, LLM-4, LLM-5 | chat_process_stream.go, fallback.go, chat_events.go | med | deadline/stub-429/slow-reader tests |
| 9 | HTTP-2 shared stats ticker, CACHE-1 singleflight | statistics, gastronomy.go | low | call-count tests |
| 10 | DB-3/4/5 N+1 and limits; then MEASURE items | trip, recents, poi repos | med | query-count tracer, staging EXPLAIN |

Roll back any item by reverting its commit. None of the items needs a migration except the proposed indexes (`DROP INDEX CONCURRENTLY`).
