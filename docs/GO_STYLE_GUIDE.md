# Go Style & Code-Quality Guide — loci-connect-server

This guide is **specific to this repository**. It is not a generic "best practices" dump — every
"good" and "bad" example below is real code from this codebase, cited by `file:line`. The intent is
to make the patterns we already do well explicit, and to name the handful of things we should stop
doing.

**Honest baseline:** this codebase is already in good shape on the fundamentals — `log/slog`
everywhere, manual constructor DI with no globals, sentinel errors with `%w` wrapping, `errgroup` /
`safego` for concurrency, mutex-protected shared state, and a real test setup (`pgxmock`, hand-written
fakes, integration tests). Most of the work here is *consistency* and *taming a few oversized files*,
not a rewrite.

Tooling baseline is already enforced by [`.golangci.yml`](../.golangci.yml): `gofumpt` (with
`extra-rules`), `govet`, `staticcheck` (all checks), and `revive`. **Run `golangci-lint run` before
every push.** This guide assumes that baseline and adds judgment on top of it.

Module path: `github.com/FACorreiaa/loci-connect-api`.

---

## 1. Idiomatic Go & Code Standards

### Key principles and why they matter

- **Clarity over cleverness.** Code is read far more than written. The reader is a teammate at 2am
  during an incident.
- **Small, single-purpose packages named for what they provide.** A package name is part of every
  identifier that uses it.
- **Accept interfaces, return structs.** Keep the surface area a caller depends on minimal.
- **Format and lint are non-negotiable.** `gofumpt` + the linters in `.golangci.yml` settle all style
  debates so reviews can focus on substance.

### Anti-patterns to eliminate

- Generic package names that carry no domain meaning (`utils`, `helpers`, `common`, `list`).
- Files that grow into monoliths because "it's all related" (see §4).
- Stuttering identifiers (`poi.POIService`) — already largely avoided here, keep it that way.

### Good vs bad (from this repo)

**Good — consistent domain layout.** `internal/domain/auth/` is the template:
`handler/`, `service/`, `repository/`, `presenter/`, `common/`, each file mapping to one concern.
Domains import *inward* only (chat → poi/city/interests; nothing imports chat back), so there are no
cycles.

**Good — no stuttering, domain-specific package names.** `auth`, `chat`, `poi`, `city`, `discover`,
`profiles`, `statistics` — all lowercase, all meaningful.

**Bad — a generically named package.** `internal/domain/list/` is so generic every caller has to
alias it: `itinerarylist "…/internal/domain/list"`. If the import always needs a rename, the package
is misnamed. → rename to `itinerary`.

**Bad — structural sprawl.** `internal/domain/chat/service/` has 11 files; `internal/domain/poi/poi_service.go`
is a ~64KB monolith. "Related" is not the same as "one package, one file." See §4.

### Audit checklist

- [ ] `golangci-lint run` is clean (no per-line `//nolint` without a reason comment).
- [ ] No package named `utils` / `helpers` / `common-as-a-dumping-ground`.
- [ ] No import that *always* needs an alias (signals a misnamed package).
- [ ] Exported identifiers have doc comments starting with the identifier name.
- [ ] No source file over ~800 lines without a deliberate reason.

---

## 2. Logging

### Key principles and why they matter

- **One logging library, injected, never global.** Globals make tests flaky and ownership unclear.
- **Structured key/value logging** so logs are queryable, not grepped.
- **Context-aware logging** (`*Context` methods) so a request's logs can be correlated.
- **Log levels mean something:** `Error` = someone gets paged-worthy signal; `Warn` = degraded but
  served; `Info` = lifecycle/business events; `Debug` = off in prod.

### Anti-patterns to eliminate

- `fmt.Println` / `fmt.Printf` as logging — no level, no structure, no context, can't be turned off.
- **Logging secrets.** Ever.
- A package-level logger singleton or `slog.Default()` reached for implicitly.

### Good vs bad (from this repo)

**Good — the house pattern.** A single JSON logger is built once and injected down the tree:

```go
// cmd/server/main.go:30
logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelInfo,
}))
// …passed to InitDependencies(cfg, logger) and threaded into every service/repo constructor
```

**Good — per-operation context with `With`:**

```go
// internal/domain/statistics/handler.go:54
l := h.logger.With(slog.String("method", "GetMainPageStatistics"))
```

**Bad — `fmt.Println` in worker goroutines.** These dump raw, unleveled, contextless text (and in
two cases, **a secret**):

```go
// internal/domain/poi/poi_service.go:91-92  ← LEAKS API KEY, must be removed
fmt.Println("Model: \n", model)
fmt.Println("API Key: \n", apiKey)

// internal/domain/poi/poi_service.go:1032, 1486, 1587, 1688, 1789
fmt.Println(cleanTxt)
```

Replace with `s.logger.DebugContext(ctx, "llm response", slog.String("text", cleanTxt))` (and delete
the API-key print outright).

### What to log vs not

| Log | Don't log |
|-----|-----------|
| Request start/end + latency + status | Passwords, tokens, API keys, full PII |
| Business events (user registered, payment captured) | Raw LLM payloads at `Info` (use `Debug`) |
| Errors with wrapped context | Per-iteration logs inside hot loops |
| Degradation (`Warn`) with a metric alongside | Anything you'd be uncomfortable seeing in a leaked log dump |

### Improvements to adopt

- **Request/correlation IDs.** We have OTel spans on hot paths but no request ID in the slog output. Add
  a middleware that generates/reads a request ID, stores it in `context`, and a small helper that
  pulls it into a `logger.With(slog.String("request_id", id))` so all logs for a request correlate.
- **Pair degradation logs with metrics.** Warn-and-continue paths (`chat_service.go:1357`,
  `chat_service.go:1379`) silently degrade. Keep the `WarnContext`, but increment a counter so the
  rate is visible and alertable.

### slog vs zap (trade-off, honest)

`log/slog` is stdlib, zero-dependency, and already idiomatic here. zap is faster under extreme log
volume but adds a dependency and an API to learn. **Verdict: stay on slog.** If a specific hot path is
ever shown by profiling to be logging-bound, switch *that* path to `slog.LogAttrs` (no `any` boxing)
before reaching for zap.

### Audit checklist

- [ ] Zero `fmt.Print*` used as logging in non-`main` code.
- [ ] No secret ever passed to a logger or `fmt.Print*`.
- [ ] Logger is always a constructor parameter, never a package global or `slog.Default()`.
- [ ] `*Context` variants (`InfoContext`, `WarnContext`, `ErrorContext`) used where a `ctx` exists.
- [ ] Every `Warn`-and-continue has a metric next to it.

---

## 3. Dependency Injection

### Key principles and why they matter

- **Manual constructor injection.** Explicit wiring is greppable and needs no framework or codegen.
- **Small, consumer-owned interfaces.** The consumer declares the narrow interface it needs; the
  provider returns a concrete struct.
- **No service locators, no globals, no `init()` wiring.** Dependencies are visible in the signature.

### Anti-patterns to eliminate

- Fat interfaces with 15+ methods — they force fake-everything tests and couple unrelated callers.
- Inconsistent injection (some constructors take a logger, peers don't).

### Good vs bad (from this repo)

**Good — explicit wiring hub.** `cmd/api/dependencies.go` wires the whole app in clear phases:
`initDatabase → initRepositories → initServices → initHandlers`. No magic.

**Good — small consumer-owned interface:**

```go
// internal/domain/statistics/repository.go
type Repository interface {
    GetMainPageStatistics(ctx context.Context, userID uuid.UUID) (...)
    GetDetailedPOIStatistics(ctx context.Context, userID uuid.UUID) (...)
    LandingPageStatistics(ctx context.Context, userID uuid.UUID) (...)
}
```

**Bad — fat interface.** `internal/domain/chat/service/chat_service.go:80`
`type LlmInteractiontService interface` carries 16+ methods. Split along usage seams (session
lifecycle vs streaming vs bookmarks) so callers depend only on what they use.

**Bad — inconsistent logger injection.** Most handlers take a logger; `AuthHandler` does not:

```go
// internal/domain/auth/handler/auth_handler.go:25
func NewAuthHandler(svc *service.AuthService) *AuthHandler { … }   // no logger
// vs dependencies.go: chathandler.NewChatHandler(d.ChatService, d.Logger)
```

Standardize: every handler/service/repository constructor takes `*slog.Logger`.

### When (if ever) to introduce Wire

Not now. The wiring in `dependencies.go` is readable and the dependency count is manageable. Reach for
google/wire only if manual wiring becomes error-prone *and* the graph is large enough that the codegen
pays for its own indirection. Today it would add a build step and obscure the explicit graph.

### Audit checklist

- [ ] Every dependency is a constructor parameter (no globals, no `init()`).
- [ ] Interfaces are defined where they're consumed, not where they're implemented.
- [ ] No interface exceeds ~7–8 methods without a clear reason; split by usage seam otherwise.
- [ ] Every handler/service/repo constructor injects the logger.
- [ ] Constructors that can fail return `error` — they never `panic` or `log.Fatal` (see §4/§5).

---

## 4. Avoiding Monster Functions

### Key principles and why they matter

- **One function, one job.** A function you can't summarize in a sentence is doing too much.
- **Guideline, not law: ~50–80 lines.** Past that, look hard for extraction seams. Length itself
  isn't the bug — *mixed responsibilities* are.
- **Extract by responsibility,** not arbitrarily by line count.

### Anti-patterns to eliminate

- Functions that parse input + apply business rules + hit the DB + format the response all in one.
- 6-levels-deep nesting; giant `switch` arms with parallel inline logic.

### Good vs bad (from this repo)

The longest functions, all in the chat/poi domains:

| Lines | Function | Location |
|------:|----------|----------|
| 283 | `ContinueSessionStreamed` | `chat/service/chat_service.go:1308` |
| 273 | `getUserPreferencesPrompt` | `chat/service/chat_prompt.go:10` |
| 252 | `GetUserChatSessions` | `chat/repository/chat_repository.go:946` |
| 221 | `orchestrateLLMStreams` | `chat/service/chat_process_stream.go:180` |
| 186 | `SavePOIDetails` | `poi/poi_repository.go:773` |

**Bad — `ContinueSessionStreamed` (283 lines, ~6 nesting levels, 28 `if`s, 13 loops).** It does
session fetch + city resolution (with fuzzy fallback) + message persistence + intent classification +
POI generation + a large intent `switch` + stream emission + caching — all inline.

**Before (shape):**

```go
func (l *ServiceImpl) ContinueSessionStreamed(ctx, sessionID, message, userLocation, eventCh) error {
    // 1. fetch + validate session ............ ~30 lines
    // 2. resolve city (+ fuzzy fallback) ...... ~40 lines
    // 3. persist user message ................. ~20 lines
    // 4. classify intent (LLM) ................ ~25 lines
    // 5. generate semantic POIs ............... ~40 lines
    // 6. big switch on intent ................. ~80 lines
    // 7. format + cache + emit ................ ~40 lines
}
```

**After (shape) — orchestrator delegates to named steps, each independently testable:**

```go
func (l *ServiceImpl) ContinueSessionStreamed(ctx context.Context, p ContinueParams) error {
    session, err := l.loadSession(ctx, p.SessionID)
    if err != nil { return l.emitError(ctx, p.EventCh, err) }

    city, err := l.resolveCity(ctx, session, p.UserLocation)
    if err != nil { return l.emitError(ctx, p.EventCh, err) }

    if err := l.persistUserMessage(ctx, session, p.Message); err != nil {
        l.logger.WarnContext(ctx, "user message not persisted, continuing in-memory",
            slog.Any("error", err))
        // metric: chat_user_msg_persist_failures_total++
    }

    intent, err := l.classifyIntent(ctx, session, p.Message)
    if err != nil { return l.emitError(ctx, p.EventCh, err) }

    return l.routeIntent(ctx, intent, session, city, p.EventCh)
}
```

Each extracted method (`loadSession`, `resolveCity`, `persistUserMessage`, `classifyIntent`,
`routeIntent`) is unit-testable in isolation; `emitError` centralizes the error→event conversion
(and fixes §5/G4).

**`getUserPreferencesPrompt` (273 lines)** is a different smell: one enormous string template. Extract
sub-prompts into named builder functions (or `text/template` files) and compose them.

### Audit checklist

- [ ] Can you name a function's single responsibility in one sentence? If not, split it.
- [ ] No function mixes transport + business logic + persistence + formatting.
- [ ] Nesting depth ≤ 3–4; use early returns and guard clauses.
- [ ] Big `switch` arms delegate to functions rather than inlining logic.
- [ ] Files over ~800 lines have a plan to split (`poi_service.go`, `poi_repository.go`, `chat_repository.go`).

---

## 5. Other Essential Best Practices

### Error handling

**Already good — sentinels + `%w` wrapping + `errors.Is/As`:**

```go
// internal/types/errors.go
var ErrNotFound = errors.New("requested item not found")

// wrapping with context (chat_service.go:555)
return nil, fmt.Errorf("failed to get user chat sessions: %w", err)

// routing on sentinels (chat_handler.go:196+) and pg codes (profile_repository.go:352)
switch {
case errors.Is(err, common.ErrChatNotFound): …
}
if errors.As(err, &pgErr) && pgErr.Code == "23505" { … } // unique violation
```

**Bad — `panic` / `log.Fatal` inside a constructor** (`NewLlmInteractiontService`):

```go
// internal/domain/chat/service/chat_service.go:154
aiClient, err := generativeAI.NewGeminiChatClient(ctx, apiKey, model)
if err != nil {
    panic(err)          // ← should return error
}
// …a few lines later:
log.Fatalf("Failed to create embedding service: %v", err) // ← also kills the process
```

Constructors must return `error`. Let `main` decide whether a failure is fatal.

**Bad — error chain lost at the stream boundary.** Errors are flattened to strings before going on the
event channel, so the client can't `errors.Is/As`:

```go
// chat_service.go:1325, 1330, 1346, 1367, 1524, 1912…
l.sendEvent(ctx, eventCh, locitypes.StreamEvent{Type: EventTypeError, Error: err.Error(), …})
```

Give `StreamEvent` (`internal/types/chat_session.go:197`) a typed error field (or a `Code` derived from
the sentinel) so downstream can classify, while still carrying a user-facing message.

**Bad — fragile re-panic transaction pattern** (`chat_repository.go:206-208`): a deferred
`recover()`/`panic(p)` to drive rollback. Extract a reusable `withTx(ctx, fn)` helper that does
begin/commit/rollback once, correctly, in one place.

### Testing

**Already good:** `pgxmock` for DB, hand-written fakes (`auth/servicetest/helpers.go`),
`testify/require`, 7 `*_integration_test.go`.

**Improve — table-driven tests.** Current tests are one function per case (verbose,
`auth/service/auth_service_test.go:24+`). Prefer:

```go
func TestRegisterUser(t *testing.T) {
    tests := []struct {
        name    string
        setup   func(*fakeRepo)
        params  service.RegisterParams
        wantErr error
    }{
        {"success", func(r *fakeRepo){…}, validParams, nil},
        {"duplicate email", func(r *fakeRepo){…}, validParams, common.ErrUserAlreadyExists},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) { … })
    }
}
```

**Improve — coverage gaps.** No tests in: `admin`, `payment`, `subscription`, `share`, `export`,
`favorites`, `custom_auth`. Prioritize money-handling domains (`payment`, `subscription`, `stripe`).

### Concurrency

**Already good:** `pkg/concurrency/safego.go` (panic-recovering goroutine + `WaitGroup`), `errgroup`
in profiles, mutex-protected shared maps (`chat_process_stream.go:202`), `context.Context` threaded
everywhere, no `context.TODO()`.

**Improve — one raw `go` bypasses the helper:**

```go
// cmd/server/main.go:59
go startPprofServer(cfg, logger)   // unrecovered panic here is silent; route via safego
```

### Audit checklist

- [ ] All errors wrapped with `%w` and contextual message as they bubble up.
- [ ] Sentinels/typed errors at boundaries; no error flattened to a string before a consumer needs it.
- [ ] Zero `panic` / `log.Fatal` outside `main`/startup; constructors return `error`.
- [ ] Transactions go through one `withTx` helper, not ad-hoc defer/recover.
- [ ] New tests are table-driven; money-handling domains have coverage.
- [ ] Every goroutine has bounded lifetime and panic recovery (`safego` / `errgroup`).
- [ ] `context.Context` is the first parameter of every I/O-bound method.

---

## Top 10 Prioritized Improvements

Ordered by ROI (impact ÷ risk). Detail, effort, and verification per item live in
[`REFACTOR_BACKLOG.md`](./REFACTOR_BACKLOG.md).

1. **Remove the API-key `fmt.Println`** (`poi_service.go:91-92`) — secret leak. Do today.
2. **Replace remaining `fmt.Println` with `slog`** in poi workers (`:1032,1486,1587,1688,1789`).
3. **Stop `panic`/`log.Fatal` in constructors** — return `error` (`chat_service.go:154` + embedding).
4. **Route the raw `go` through `safego`** (`cmd/server/main.go:59`).
5. **Consistent handler logger injection** — give `AuthHandler` a logger; make it the rule.
6. **Typed error on `StreamEvent`** so the chat error chain survives the boundary.
7. **Add request/correlation IDs** to slog via middleware; metric the warn-and-continue paths.
8. **Split the monster functions** — `ContinueSessionStreamed`, `orchestrateLLMStreams`,
   `GetUserChatSessions`, `getUserPreferencesPrompt` (tests first, behavior-preserving).
9. **Extract a `withTx` helper** and delete the re-panic rollback pattern.
10. **Tests for untested domains** (payment/subscription/stripe first) + convert suites to
    table-driven.
