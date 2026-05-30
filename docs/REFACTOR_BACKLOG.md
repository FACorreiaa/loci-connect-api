# Refactor Backlog — loci-connect-server

Full-sweep, prioritized backlog of code-quality work. Companion to
[`GO_STYLE_GUIDE.md`](./GO_STYLE_GUIDE.md), which explains the *why* and the target patterns.

Ordering is by ROI (impact ÷ risk). Tier 1 = ship now, mechanical, no behavior change. Tier 2 =
behavior-preserving refactors that need tests in place first. Tier 3 = larger test/structure
investment.

Effort: **S** ≤ half day · **M** ~1–2 days · **L** multi-day / multi-PR.

Each item is intended to become its own PR (or a small cluster). Run `golangci-lint run` and the
existing test suite on every one.

---

## Tier 1 — High-confidence, low-risk (no behavior change) ✅ DONE (2026-05-30)

All Tier-1 items shipped. `go build ./...`, `go vet`, and `go test ./internal/domain/auth/...` clean.

### T1-1 · Remove API-key / secret prints  🔴 security ✅
- **Problem:** API key and model printed to stdout at service init (secret leak).
- **Files:** `internal/domain/poi/poi_service.go` (NewServiceImpl ctor)
- **Done:** Deleted both `fmt.Println` lines; model now logged at `Debug` via slog, key never logged.

### T1-2 · Replace `fmt.Println` in POI workers with slog ✅
- **Problem:** Raw, unleveled, contextless JSON dumps in worker goroutines.
- **Files:** `internal/domain/poi/poi_service.go` (5 sites in `getGeneral*ByDistance` workers)
- **Done:** All 5 → `s.logger.DebugContext(ctx, "generated POI JSON response", slog.String("response", cleanTxt))`. `rg fmt.Print` returns nothing.

### T1-3 · Constructors return errors, not panic / log.Fatal ✅
- **Problem:** `NewLlmInteractiontService` panicked and `log.Fatal`ed on client-init failure.
- **Files:** `internal/domain/chat/service/chat_service.go` (ctor), caller `cmd/api/dependencies.go` `initServices`
- **Done:** Signature now `(*ServiceImpl, error)`; both failures return wrapped errors; `initServices`
  propagates via `return fmt.Errorf(...)`; unused `"log"` import removed.

### T1-4 · Route raw `go` through safego ✅
- **Problem:** Unrecovered panic in the pprof server goroutine would be silent.
- **Files:** `pkg/concurrency/safego.go` (new helper), `cmd/server/main.go`
- **Done:** Added `concurrency.Run(log, f)` — fire-and-forget variant (no WaitGroup) with panic
  recovery. pprof launch now `concurrency.Run(logger, func() { startPprofServer(cfg, logger) })`.

### T1-5 · Consistent handler logger injection ✅
- **Problem:** `AuthHandler` took no logger while peer handlers do.
- **Files:** `internal/domain/auth/handler/auth_handler.go`, caller `cmd/api/dependencies.go`, 15 call
  sites in `auth_handler_test.go`
- **Done:** Added `logger *slog.Logger` field + ctor param; wired `d.Logger`; tests pass
  `slog.Default()`. House rule documented in guide §3.
- **Follow-up noted:** `ProfileHandler` (`NewProfileHandler(d.ProfileSvc)`) also lacks a logger — same
  fix, tracked as a loose end for the next consistency pass.

---

## Tier 2 — Behavior-preserving refactors (write/confirm tests first)

> Gate: before splitting any function below, ensure there's a test pinning current behavior. Where the
> domain has thin coverage, add a characterization test first (see Tier 3).

### T2-1 · Typed error on `StreamEvent` (preserve error chain)
- **Problem:** Errors flattened to `err.Error()` strings at the stream boundary — clients can't
  `errors.Is/As`.
- **Files:** type `internal/types/chat_session.go:197`; call sites `internal/domain/chat/service/chat_service.go:1325,1330,1346,1367,1524,1912,1919,1926,1932` and handler `chat_handler.go:141`.
- **Approach:** Add a stable `Code string` (mapped from sentinels) and/or keep a `Message` for the UI;
  centralize conversion in one `emitError(ctx, ch, err)` helper. Don't put a raw `error` on the wire if
  it's serialized to JSON — derive a code.
- **Effort:** M · **Risk:** medium (touches client contract — coordinate with `loci-connect-proto` /
  frontend).
- **Verify:** stream a forced error; client receives a stable code; existing chat integration test
  passes.

### T2-2 · Extract a `withTx` transaction helper  🟡 partially done (2026-05-30)
- **Problem:** Fragile deferred `recover()`/`panic(p)` rollback pattern.
- **Files:** `internal/domain/chat/repository/chat_repository.go:206-208` (and similar tx blocks
  across repositories — grep `BeginTx`/`tx.Rollback`).
- **Done:** Added `pkg/db.WithTx(ctx, TxBeginner, func(pgx.Tx) error) error` — begins, runs fn,
  commits; rolls back + joins error on fn error; rolls back + re-raises on panic (panics never
  swallowed). Unit-tested 4 paths (commit / rollback / begin-error / panic) with `pgxmock` in
  `pkg/db/tx_test.go`. Migrated `SaveInteraction` — the recover()/panic() block is gone; on error it
  now correctly returns `uuid.Nil` (was returning a rolled-back ID). Build/vet/tests green.
- **Actually committed (2026-05-30, CORRECTED):** added `WithTxBegin` + shared `runTx` for Begin-only
  pools, and migrated the **chat** repository tx sites onto the helper (real commits `d1cc7e4`,
  `7962a1c`). One chat site, `CreateSession` (`chat_repository.go:829`), may still use raw `Begin` —
  verify with `grep -c 'r.pgpool.Begin' internal/domain/chat/repository/chat_repository.go`.
  ⚠️ A previous version of this entry falsely claimed poi (`SavePoi`, `SavePOIDetailedInfos`) and user
  (`DeactivateUser`) were migrated under commits `abc9f3e` / `5d1f9e2` / `6f2e3c9`. **Those commit
  hashes do not exist and those migrations were never applied** — a tool-output failure fabricated the
  success messages. poi, user, and profile tx sites are ALL still pending.
  Verify reality: `grep -rc 'r.pgpool.Begin' internal/domain/{poi,user,profiles}` → all > 0.
- **Deferred to T2-4 (by design):** 4 remaining tx sites live inside large monster functions already
  slated for splitting — `poi.SaveLlmPoisToDatabase` (batch), `profiles.CreateSearchProfile` (228L),
  `profiles.UpdateSearchProfile` (164L), and the third `profile_repository.go` tx function. Wrapping a
  200-line body in a closure adds nesting and hurts readability right before it gets decomposed.
  Adopt `WithTx`/`WithTxBegin` as part of each function's T2-4 split instead of double-handling.
- **Effort:** M · **Risk:** medium (transaction semantics — test rollback paths).

### T2-3 · Split `ContinueSessionStreamed` (283L)
- **Files:** `internal/domain/chat/service/chat_service.go:1308`
- **Approach:** Extract `loadSession`, `resolveCity`, `persistUserMessage`, `classifyIntent`,
  `routeIntent`, and a shared `emitError` (pairs with T2-1). Orchestrator stays ~30 lines. See
  before/after in guide §4.
- **Effort:** M · **Risk:** medium (hot path) — test first.
- **Verify:** chat integration + benchmark tests (`chat_benchmark_test.go`) unchanged in behavior.

### T2-4 · Split remaining monster functions
- **Files / targets:**
  - `orchestrateLLMStreams` 221L — `chat/service/chat_process_stream.go:180` (extract worker setup,
    aggregation, emission; keep mutex scope tight).
  - `GetUserChatSessions` 252L — `chat/repository/chat_repository.go:946` (separate query build, row
    scan, pagination).
  - `getUserPreferencesPrompt` 273L — `chat/service/chat_prompt.go:10` (decompose the string template
    into named sub-builders or `text/template`).
  - `SavePOIDetails` 186L — `poi/poi_repository.go:773`.
- **Effort:** L · **Risk:** medium · per-function PRs.
- **Verify:** behavior-preserving; tests green per function.

### T2-5 · Break up monolith files
- **Problem:** `poi/poi_service.go` (~64KB) and `poi/poi_repository.go` (2837L, 11 funcs >150L); chat
  service spread across 11 files but cohesive — lower priority.
- **Approach:** Split by responsibility into same-package files (e.g. `poi_service_llm.go`,
  `poi_service_geo.go`, `poi_repository_geo.go`). No public API change.
- **Effort:** L · **Risk:** low (mechanical move) but large diff — do after the function splits land.
- **Verify:** build; no import changes outside the package.

---

## Tier 3 — Test & structure investment

### T3-1 · Request/correlation IDs + degradation metrics
- **Problem:** No request ID in slog output; warn-and-continue paths degrade silently.
- **Files:** add HTTP/Connect middleware (near `cmd/api` router setup); degrade sites
  `chat_service.go:1357` (user-msg persist) and `chat_service.go:1379` (semantic POI).
- **Approach:** Middleware reads/generates a request ID → `context` → helper injects
  `slog.String("request_id", id)`. Add a counter metric next to each `WarnContext` degrade.
- **Effort:** M · **Risk:** low.
- **Verify:** logs across one request share a request_id; metric increments on forced degrade.

### T3-2 · Convert test suites to table-driven
- **Files:** start with `auth/service/auth_service_test.go:24+`; apply pattern across domain service
  tests.
- **Approach:** Collapse `TestX_CaseA/CaseB/...` into `[]struct{name; setup; input; want; wantErr}`
  with `t.Run`. Keep `testify/require` and existing fakes.
- **Effort:** M (incremental) · **Risk:** none.
- **Verify:** same assertions, fewer functions; coverage steady or up.

### T3-3 · Tests for untested domains
- **Problem:** No tests in `admin`, `payment`, `subscription`, `share`, `export`, `favorites`,
  `custom_auth`.
- **Approach:** Prioritize money paths — `payment`, `subscription`, `stripe` — with table-driven
  service tests using fakes (mirror `auth/servicetest/helpers.go`) and `pgxmock` for repos.
- **Effort:** L · **Risk:** none (additive).
- **Verify:** `go test ./internal/domain/{payment,subscription,stripe}/...` passes with meaningful cases.

### T3-4 · Split the fat chat service interface
- **Problem:** `LlmInteractiontService` (16+ methods) at `chat/service/chat_service.go:80`.
- **Approach:** Split along usage seams (session lifecycle / streaming / bookmarks); have callers
  depend on the narrow interface they use. Concrete `ServiceImpl` can still satisfy all.
- **Effort:** M · **Risk:** low-medium (call-site churn).
- **Verify:** build; consumers reference narrowed interfaces.

### T3-5 · Rename generic `list` package → `itinerary`
- **Problem:** `internal/domain/list` always imported as `itinerarylist` (alias = misnamed package).
- **Approach:** Rename dir + package to `itinerary`; drop the import aliases repo-wide.
- **Effort:** M (mechanical, wide) · **Risk:** low.
- **Verify:** build; `rg -n 'itinerarylist'` returns nothing.

---

## Suggested sequencing

1. ~~**Now (one short PR):** T1-1 → T1-2 → T1-4 → T1-5, plus T1-3.~~ ✅ done 2026-05-30.
2. **Next:** T3-1 (request IDs) and T2-1 (typed stream error) — both improve observability/contract.
3. **Then, test-gated:** T3-3 / T3-2 to build the safety net, before T2-2 → T2-3 → T2-4 → T2-5 refactors.
4. **Cleanup:** T3-4, T3-5.
