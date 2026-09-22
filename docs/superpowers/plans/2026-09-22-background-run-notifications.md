# Background Run Notifications Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When a search finishes or fails while the person is elsewhere — another page, a background tab, or with the tab closed — tell them, and take them back to the result in one click.

**Architecture:**
- The server records every generation in a `generation_runs` table, caps each user at 3 concurrent runs (an interceptor that runs before quota), and sends a VAPID web push when a run finishes.
- The web client tracks up to 3 runs in a registry, shows an app-wide toast when one finishes off-page, and relays pushes through a small service-worker add-on.
- Result pages load finished sessions from the server, so a notification that opens a fresh tab never re-runs the search.

**Tech Stack:**
- Go: Connect RPC, pgx/v5, goose migrations, testify, `github.com/SherClockHolmes/webpush-go`.
- Protobuf: buf and BSR.
- Client: SolidStart v2, `@connectrpc/connect`, solid-query, vitest + happy-dom, vite-plugin-pwa (`generateSW` + `workbox.importScripts`).

**Spec:** `docs/superpowers/specs/2026-09-22-background-run-notifications-design.md` (same branch). Read it first. The "Deviations from the spec" section below overrides it where they differ.

## Global Constraints

- Concurrent run cap: **3** per user. A `running` row older than **10 minutes** is stale: it reads as `failed` with `error_code = "deadline_exceeded"` and does not count toward the cap.
- The cap refusal is `connect.CodeResourceExhausted` with message exactly `You have 3 searches running — wait for one to finish`, and it is returned **before** the quota interceptor, so a refused search consumes no quota.
- Push payload keys, exactly: `sessionId`, `cityName`, `domain`, `status` (`"done"` | `"failed"`), `title`, `body`, `url`.
- Titles: `Your {City} {noun} is ready` / `Your {City} {noun} didn't finish`. Bodies: `Tap to open it.` / `Tap to try again.`
  - Nouns by domain: itinerary/general → `itinerary`, activities → `activities`, accommodation → `hotels`, dining → `restaurants`, nearby → `nearby places`.
  - With no city: `Your {noun} is ready`.
- Web push: TTL `3600`, urgency `high`. A `404`/`410` response deletes the device.
- Env: `VAPID_PUBLIC_KEY`, `VAPID_PRIVATE_KEY`, `VAPID_SUBJECT`. If any is empty, sending is skipped (logged once at startup) and nothing else changes.
- Notification setting: `notification_settings.search_finished BOOLEAN NOT NULL DEFAULT TRUE`.
- Permission prompt: "Not now" is remembered in `localStorage` key `loci.pushPrompt.dismissedAt` for **30 days**.
- Migrations: take the next free number **at merge time** (`ls pkg/db/migrations | sort | tail -1`). `make` runs a `check-migrations` pre-commit hook that rejects duplicates. The plan calls them `NNNN_generation_runs` and `MMMM_push_devices`.
- Generated code is committed. Regenerate protos only with `buf generate` (pinned plugins).
- Client: never run repo-wide `pnpm format`; format only the files you touched (`pnpm exec oxfmt <files>`).
- Other Claude sessions commit in the same checkouts. Work in worktrees, stage files explicitly, never `git add -A`.

## Deviations from the spec (decided while planning, from reading the code)

1. **The cap lives in an interceptor with a reservation, not in the handler.** Quota is spent by `subscription.RateLimitInterceptor` before the handler runs, and there is no refund, so a handler-side cap would charge quota for a refused search.
   - A new `runs.CapInterceptor` sits between `userRateLimiter` and `subscriptionInterceptor`. It peeks at the first `ChatRequest`, skips resumes (`resume_token` set), and reserves a run row.
   - The row's primary key is a run `id`. `session_id` is `UNIQUE NULL` and is attached when the start event arrives, because the session id is minted inside the pipeline.
2. **Run status is written by a `runs.Tracker` that the handler feeds with every event** — both the live loop and the post-disconnect drain goroutine — rather than inside the service pipeline. `COMPLETE` is emitted after `persistGenerations` (chat_process_stream.go:565) and `UpdateSession` (:695), so `COMPLETE` seen means "saved".
3. **No duplicate-event-id fix.** The four `EventID: recommendationRunID` sites (chat_process_stream.go:646/658/669/683) are cases of one `switch`, so exactly one is sent per run and ids are already unique.
4. **Service worker: `workbox.importScripts`, not `injectManifest`.** The existing `generateSW` config (size-filtered precache, `additionalManifestEntries`) stays untouched. `public/push-sw.js` is imported into the generated worker.
5. **Result pages load from the server.** Today `/activities`, `/hotels` and `/restaurants` restore only from the live store or a single `sessionStorage` slot, and otherwise re-run the search. With 3 runs the slot is overwritten, and a notification that opens a fresh tab would re-run (and re-charge) the search.
   - Completed sessions become a keyed map in `sessionStorage`.
   - The list pages fall back to `GetChatSession` plus `GetSessionPOIs` before re-running anything.
   - `/nearme` gets the same fallback (`SESSION_POI_SECTION_GENERAL`).
6. **The result URL is built in one place on the server** (`runs.ResultPath`), used by both `sendCompletionEvent` and the push payload.

---

## File structure

**loci-connect-proto**
- Modify `proto/loci/chat/chat.proto`:
  - `GetRunStatus` RPC and messages.
  - `RunStatus` enum.
  - `CompletePayload.load_from_session`.
- Modify `proto/loci/user/user.proto`:
  - `RegisterPushDevice`, `UnregisterPushDevice` and `GetPushConfig`.
  - `PushPlatform` enum.
  - `search_finished` on the settings messages.

**loci-connect-server**
- `pkg/db/migrations/NNNN_generation_runs.up.sql`: run table.
- `internal/domain/runs/`: new package, one responsibility per file.
  - `runs.go`: `Run`, `Status`, `ErrAtCapacity`, constants.
  - `store.go`: `Store` interface + `PostgresStore`.
  - `store_integration_test.go`
  - `resultpath.go` + `resultpath_test.go`: the one URL builder.
  - `reservation.go`: context plumbing for a reserved run.
  - `cap_interceptor.go` + `cap_interceptor_test.go`
  - `tracker.go` + `tracker_test.go`: turns stream events into status writes.
  - `handler.go` + `handler_test.go`: `GetRunStatus`.
- `internal/domain/chat/resumebuf/resumebuf.go`: add `Subscribe`.
- `internal/domain/chat/handler/chat_handler.go`: tracker wiring, follow-live resume, buffer-gone terminal.
- `internal/domain/chat/service/chat_process_stream.go`: use `runs.ResultPath`.
- `cmd/api/dependencies.go`, `cmd/api/router.go`: wiring.
- `pkg/db/migrations/MMMM_push_devices.up.sql`: device table + `search_finished`.
- `internal/domain/push/`: new package.
  - `devices.go`: device store.
  - `message.go` + `message_test.go`: payload text.
  - `sender.go` + `sender_test.go`: webpush delivery.
  - `notifier.go` + `notifier_test.go`: run finished → claim → settings → send.
- `internal/domain/user/…`: `search_finished` plumbing; push RPC handlers.
- `pkg/config/config.go`: `PushConfig`.

**loci-client**
- `src/lib/api/llm.ts`: `getSessionList`, `getRunStatuses`.
- `src/lib/streaming/completed-sessions.ts` (new): keyed completed-session map. `restore-session.ts` reads it.
- `src/lib/streaming/live-stream-store.ts`: multi-run registry.
- `src/lib/streaming-service.ts`: one `Run` per session.
- `src/lib/streaming/resume-live.ts`: resume every envelope.
- `src/lib/hooks/useChatRPC.ts`: report phases to the registry.
- `src/lib/streaming/hydrate-session.ts` (new): server fallback for the result pages.
- `src/routes/{activities,hotels,restaurants,nearme}/index.tsx`: use it.
- `src/components/toast/Toaster.tsx` + `src/lib/toast-store.ts` (new).
- `src/components/runs/RunWatcher.tsx` (new) + `src/lib/runs/watcher-rules.ts` (new, pure).
- `src/app.tsx`: mount `Toaster` + `RunWatcher`.
- `src/lib/push/push-client.ts` (new): subscribe and register.
- `src/lib/push/prompt-rules.ts` (new, pure).
- `public/push-sw.js` (new), `vite.config.ts` (`importScripts`).
- `src/lib/api/notifications.ts`, `src/components/features/Settings/NotificationSettings.tsx`: the "Search finished" switch.

---

## Phase 0 — Proto

### Task 1: Contracts for run status, push devices and the new setting

**Files:**
- Modify: `loci-connect-proto/proto/loci/chat/chat.proto` (service block ~line 890; `CompletePayload` at 539)
- Modify: `loci-connect-proto/proto/loci/user/user.proto` (settings messages 353-365; service 368-379)
- Regenerate: `gen/**`

**Interfaces:**
- Produces:
  - `ChatService.GetRunStatus(GetRunStatusRequest{repeated string session_ids}) → GetRunStatusResponse{repeated RunInfo runs}`
  - `RunInfo{session_id, domain (DomainType), city_name, status (RunStatus), error_code, finished_at, url}`
  - `RunStatus{UNSPECIFIED, RUNNING, DONE, FAILED}`
  - `CompletePayload.load_from_session` (bool, field 3)
  - `UserService.RegisterPushDevice(RegisterPushDeviceRequest{platform, endpoint, p256dh, auth}) → loci.common.Response`
  - `UserService.UnregisterPushDevice(UnregisterPushDeviceRequest{endpoint}) → loci.common.Response`
  - `UserService.GetPushConfig(GetPushConfigRequest{}) → PushConfig{vapid_public_key}`
  - `PushPlatform{UNSPECIFIED, WEB_PUSH, APNS}`
  - `NotificationSettings.search_finished` (4), `UpdateNotificationSettingsRequest.search_finished` (optional, 3)

- [ ] **Step 1: Branch in a worktree**

```bash
cd ~/Work/production/apps/Loci/loci-connect-proto
git fetch origin --tags
git worktree add -b feat/run-notifications ../.wt-proto-runs origin/main
cd ../.wt-proto-runs
```

- [ ] **Step 2: Add the chat contracts.** In `chat.proto`, change `CompletePayload` to:

```proto
message CompletePayload {
  string session_id = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 200
  }];
  optional AiCityResponse result = 2;
  // load_from_session is set on a resume whose buffer is gone but whose run
  // already finished: the result is not on this stream, load it with
  // GetChatSession / GetSessionPOIs instead.
  bool load_from_session = 3;
}
```

Add before `service ChatService`:

```proto
// RunStatus is where one generation is. A run that has been RUNNING for more
// than 10 minutes is reported as FAILED (deadline_exceeded).
enum RunStatus {
  RUN_STATUS_UNSPECIFIED = 0;
  RUN_STATUS_RUNNING = 1;
  RUN_STATUS_DONE = 2;
  RUN_STATUS_FAILED = 3;
}

message GetRunStatusRequest {
  repeated string session_ids = 1 [(buf.validate.field).repeated = {
    min_items: 1
    max_items: 20
    items: {
      string: {uuid: true}
    }
  }];
}

message RunInfo {
  string session_id = 1;
  DomainType domain = 2;
  string city_name = 3;
  RunStatus status = 4;
  string error_code = 5;
  google.protobuf.Timestamp finished_at = 6;
  // url is the page that shows this run's result, e.g.
  // /itinerary?sessionId=…&cityName=Crete&domain=itinerary
  string url = 7;
}

message GetRunStatusResponse {
  // Only the caller's own runs; ids that are not theirs are omitted.
  repeated RunInfo runs = 1;
}
```

In the service block, after `StreamChat`:

```proto
  // Where the caller's runs are: running, done or failed. Used after a
  // reload or on returning to the app to settle runs nobody was listening to.
  rpc GetRunStatus(GetRunStatusRequest) returns (GetRunStatusResponse);
```

- [ ] **Step 3: Add the user contracts.** In `user.proto`, add `bool search_finished = 4;` to `NotificationSettings` and `optional bool search_finished = 3;` to `UpdateNotificationSettingsRequest`. Then add:

```proto
enum PushPlatform {
  PUSH_PLATFORM_UNSPECIFIED = 0;
  PUSH_PLATFORM_WEB_PUSH = 1;
  // Reserved for the iOS app; the server does not send to it yet.
  PUSH_PLATFORM_APNS = 2;
}

message RegisterPushDeviceRequest {
  PushPlatform platform = 1 [(buf.validate.field).enum = {
    defined_only: true
    not_in: [0]
  }];
  // Web push endpoint URL, or the APNs device token.
  string endpoint = 2 [(buf.validate.field).string = {
    min_len: 1
    max_len: 2048
  }];
  // Web push only.
  string p256dh = 3 [(buf.validate.field).string = {max_len: 200}];
  string auth = 4 [(buf.validate.field).string = {max_len: 100}];
}

message UnregisterPushDeviceRequest {
  string endpoint = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 2048
  }];
}

message GetPushConfigRequest {}

message PushConfig {
  // Empty when the server has no VAPID keys: clients must not offer push.
  string vapid_public_key = 1;
}
```

And in `service UserService`, after `UpdateNotificationSettings`:

```proto
  // Push delivery: a browser (or, later, a phone) that should hear about the
  // caller's finished searches.
  rpc RegisterPushDevice(RegisterPushDeviceRequest) returns (loci.common.Response);
  rpc UnregisterPushDevice(UnregisterPushDeviceRequest) returns (loci.common.Response);
  rpc GetPushConfig(GetPushConfigRequest) returns (PushConfig);
```

- [ ] **Step 4: Lint and generate**

Run: `buf lint && buf breaking --against '.git#branch=origin/main' && buf generate`
Expected: no lint or breaking errors. `git status` shows `gen/go`, `gen/ts`, `gen/swift` and docs changes. Check that one regenerated Go file's header still names the `protoc-gen-go` version pinned in `buf.gen.yaml` (v1.36.10). If it names another, you ran a local plugin: revert and use `buf generate`.

- [ ] **Step 5: Commit, PR, merge, release both halves**

```bash
git add proto/loci/chat/chat.proto proto/loci/user/user.proto gen
git commit -m "Contracts for run status, push devices and the search-finished setting"
git push -u origin feat/run-notifications
gh pr create --fill
```

After merge:
- Check the BSR first. An inconsistent PlaceIntelligence schema has reddened main's proto job before, so run `buf push --dry-run` if supported, or check that main's proto CI is green.
- From a clean checkout of `origin/main` (`buf push` reads the working tree while `git tag` reads HEAD; they must be the same):

```bash
git switch --detach origin/main && git status --short   # must be empty
make release VERSION=v5.<next>.0   # next = one above `git tag --sort=-v:refname | head -1`
git show v5.<next>.0:proto/loci/chat/chat.proto | grep -c GetRunStatus   # expect ≥ 1
```

Record the BSR commit that `buf push` prints; client Task 11 pins it.

---

## Phase 1 — Server PR A: run records, cap, resume, GetRunStatus

Work in `~/Work/production/apps/Loci/.wt-run-notify` (branch `docs/background-run-notifications` holds the spec and plan). Rebase it on `origin/main` and continue on the same branch, or branch `feat/run-records` from it.

- [ ] **Setup: bump the proto module**

```bash
go get github.com/FACorreiaa/loci-connect-proto/v5@v5.<next>.0
go build ./...
```

If `sum.golang.org` 404s on the fresh tag, run `GOPRIVATE=github.com/FACorreiaa go get …` once. Afterwards check the go.sum line against `https://sum.golang.org/lookup/github.com/!f!a!correia!a/loci-connect-proto/v5@v5.<next>.0` once the proxy has indexed it.

### Task 2: `generation_runs` table and store

**Files:**
- Create: `pkg/db/migrations/NNNN_generation_runs.up.sql`
- Create: `internal/domain/runs/runs.go`
- Create: `internal/domain/runs/store.go`
- Test: `internal/domain/runs/store_integration_test.go`

**Interfaces:**
- Produces (package `runs`, import `github.com/FACorreiaa/loci-connect-api/internal/domain/runs`):

```go
type Status string
const (StatusRunning Status = "running"; StatusDone Status = "done"; StatusFailed Status = "failed")
const (MaxConcurrent = 3; StaleAfter = 10 * time.Minute; ErrorCodeDeadline = "deadline_exceeded")
var ErrAtCapacity = errors.New("runs: at capacity")
type Run struct { ID, UserID uuid.UUID; SessionID uuid.UUID /* uuid.Nil until attached */; Domain, CityName string; Status Status; ErrorCode string; StartedAt time.Time; FinishedAt *time.Time }
type Store interface {
	Reserve(ctx context.Context, userID uuid.UUID) (uuid.UUID, error)            // ErrAtCapacity at cap
	Release(ctx context.Context, runID uuid.UUID) error                            // deletes only an unattached row
	Attach(ctx context.Context, runID, sessionID uuid.UUID, domain, city string) error
	Finish(ctx context.Context, runID uuid.UUID, status Status, errorCode string) (Run, bool, error) // bool: this call moved it out of running
	Statuses(ctx context.Context, userID uuid.UUID, sessionIDs []uuid.UUID) ([]Run, error)
	FindBySession(ctx context.Context, userID, sessionID uuid.UUID) (Run, bool, error)
	ClaimNotification(ctx context.Context, runID uuid.UUID) (bool, error)          // true exactly once per run
}
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore
```

- [ ] **Step 1: Write the migration**

```sql
-- +goose Up
-- +goose StatementBegin
-- One row per generation, so "is my search done?" has an answer that
-- survives a closed tab, a reload, or a pod restart. The id is reserved
-- before the pipeline mints a session id (the concurrency cap needs a row to
-- count), and session_id is attached when the stream's start event arrives.
CREATE TABLE IF NOT EXISTS generation_runs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    session_id  UUID UNIQUE,
    domain      TEXT NOT NULL DEFAULT '',
    city_name   TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'done', 'failed')),
    error_code  TEXT NOT NULL DEFAULT '',
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    notified_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS generation_runs_user_running_idx
    ON generation_runs (user_id, started_at) WHERE status = 'running';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS generation_runs;
-- +goose StatementEnd
```

Apply locally using the Up half only; piping the whole file into psql also runs the Down section:
`awk '/^-- \+goose Down/{exit} {print}' pkg/db/migrations/NNNN_generation_runs.up.sql | psql "$RUNS_TEST_DSN"`

- [ ] **Step 2: Write the failing integration tests**

```go
//go:build integration

package runs

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// Run with: RUNS_TEST_DSN=postgres://... go test -tags=integration ./internal/domain/runs/

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("RUNS_TEST_DSN")
	if dsn == "" {
		t.Skip("RUNS_TEST_DSN not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func seedUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO users (id, username, email, password_hash)
		VALUES ($1, $2, $3, 'x')`, id, "runner-"+id.String()[:8], id.String()+"@example.test")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
	return id
}

func TestReserveStopsAtThree(t *testing.T) {
	pool := testPool(t)
	s := NewPostgresStore(pool)
	ctx := context.Background()
	user := seedUser(t, pool)

	for range MaxConcurrent {
		_, err := s.Reserve(ctx, user)
		require.NoError(t, err)
	}
	_, err := s.Reserve(ctx, user)
	require.ErrorIs(t, err, ErrAtCapacity)
}

// Two starts racing at two running must not both get in.
func TestReserveIsRaceSafe(t *testing.T) {
	pool := testPool(t)
	s := NewPostgresStore(pool)
	ctx := context.Background()
	user := seedUser(t, pool)
	for range MaxConcurrent - 1 {
		_, err := s.Reserve(ctx, user)
		require.NoError(t, err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, errs[i] = s.Reserve(ctx, user) }()
	}
	wg.Wait()
	ok := 0
	for _, e := range errs {
		if e == nil {
			ok++
		} else {
			require.ErrorIs(t, e, ErrAtCapacity)
		}
	}
	require.Equal(t, 1, ok)
}

func TestStaleRunDoesNotCountAndReadsFailed(t *testing.T) {
	pool := testPool(t)
	s := NewPostgresStore(pool)
	ctx := context.Background()
	user := seedUser(t, pool)

	runID, err := s.Reserve(ctx, user)
	require.NoError(t, err)
	session := uuid.New()
	require.NoError(t, s.Attach(ctx, runID, session, "itinerary", "Crete"))
	_, err = pool.Exec(ctx, `UPDATE generation_runs SET started_at = NOW() - interval '11 minutes' WHERE id = $1`, runID)
	require.NoError(t, err)

	for range MaxConcurrent {
		_, err := s.Reserve(ctx, user)
		require.NoError(t, err)
	}
	got, err := s.Statuses(ctx, user, []uuid.UUID{session})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, StatusFailed, got[0].Status)
	require.Equal(t, ErrorCodeDeadline, got[0].ErrorCode)
}

func TestFinishMovesOnceAndClaimIsExactlyOnce(t *testing.T) {
	pool := testPool(t)
	s := NewPostgresStore(pool)
	ctx := context.Background()
	user := seedUser(t, pool)
	runID, _ := s.Reserve(ctx, user)
	require.NoError(t, s.Attach(ctx, runID, uuid.New(), "itinerary", "Crete"))

	run, moved, err := s.Finish(ctx, runID, StatusDone, "")
	require.NoError(t, err)
	require.True(t, moved)
	require.Equal(t, "Crete", run.CityName)
	_, moved, err = s.Finish(ctx, runID, StatusFailed, "internal")
	require.NoError(t, err)
	require.False(t, moved, "a finished run must not be re-finished")

	var wg sync.WaitGroup
	claims := make([]bool, 2)
	for i := range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); claims[i], _ = s.ClaimNotification(ctx, runID) }()
	}
	wg.Wait()
	require.NotEqual(t, claims[0], claims[1], "exactly one claim wins")
}

func TestStatusesOnlyReturnsTheCallersRuns(t *testing.T) {
	pool := testPool(t)
	s := NewPostgresStore(pool)
	ctx := context.Background()
	alice, bob := seedUser(t, pool), seedUser(t, pool)
	runID, _ := s.Reserve(ctx, alice)
	session := uuid.New()
	require.NoError(t, s.Attach(ctx, runID, session, "itinerary", "Crete"))

	got, err := s.Statuses(ctx, bob, []uuid.UUID{session})
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestReleaseOnlyDeletesUnattachedRows(t *testing.T) {
	pool := testPool(t)
	s := NewPostgresStore(pool)
	ctx := context.Background()
	user := seedUser(t, pool)

	loose, _ := s.Reserve(ctx, user)
	require.NoError(t, s.Release(ctx, loose))
	attached, _ := s.Reserve(ctx, user)
	session := uuid.New()
	require.NoError(t, s.Attach(ctx, attached, session, "itinerary", ""))
	require.NoError(t, s.Release(ctx, attached))

	_, found, err := s.FindBySession(ctx, user, session)
	require.NoError(t, err)
	require.True(t, found)
}
```

- [ ] **Step 3: Run to verify they fail**

Run: `RUNS_TEST_DSN=$LOCAL_DSN go test -tags=integration ./internal/domain/runs/`
Expected: compile failure, `undefined: NewPostgresStore`.

- [ ] **Step 4: Implement `runs.go` and `store.go`**

`runs.go`:

```go
// Package runs records each chat generation — running, done or failed — so
// a finished search can be announced to someone who stopped watching it,
// and caps how many one person can have in flight.
package runs

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

const (
	// MaxConcurrent is how many generations one person may have running.
	MaxConcurrent = 3
	// StaleAfter is well past the pipeline's own 3–5 minute timeouts, so a
	// row still running after it belongs to a pod that died.
	StaleAfter        = 10 * time.Minute
	ErrorCodeDeadline = "deadline_exceeded"
)

// ErrAtCapacity means the caller already has MaxConcurrent runs going.
var ErrAtCapacity = errors.New("runs: at capacity")

type Run struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	SessionID  uuid.UUID // uuid.Nil until the start event attaches it
	Domain     string
	CityName   string
	Status     Status
	ErrorCode  string
	StartedAt  time.Time
	FinishedAt *time.Time
}

// effective reports a stale running row as the failure it is.
func (r Run) effective(now time.Time) Run {
	if r.Status == StatusRunning && now.Sub(r.StartedAt) > StaleAfter {
		r.Status = StatusFailed
		r.ErrorCode = ErrorCodeDeadline
	}
	return r
}
```

`store.go`:

```go
package runs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store interface {
	Reserve(ctx context.Context, userID uuid.UUID) (uuid.UUID, error)
	Release(ctx context.Context, runID uuid.UUID) error
	Attach(ctx context.Context, runID, sessionID uuid.UUID, domain, city string) error
	Finish(ctx context.Context, runID uuid.UUID, status Status, errorCode string) (Run, bool, error)
	Statuses(ctx context.Context, userID uuid.UUID, sessionIDs []uuid.UUID) ([]Run, error)
	FindBySession(ctx context.Context, userID, sessionID uuid.UUID) (Run, bool, error)
	ClaimNotification(ctx context.Context, runID uuid.UUID) (bool, error)
}

type PostgresStore struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool, now: time.Now}
}

const runColumns = `id, user_id, COALESCE(session_id, '00000000-0000-0000-0000-000000000000'::uuid),
	domain, city_name, status, error_code, started_at, finished_at`

func scanRun(row pgx.Row) (Run, error) {
	var r Run
	var status string
	err := row.Scan(&r.ID, &r.UserID, &r.SessionID, &r.Domain, &r.CityName, &status, &r.ErrorCode, &r.StartedAt, &r.FinishedAt)
	r.Status = Status(status)
	return r, err
}

// Reserve counts and inserts under a per-user transaction lock, so two
// starts racing at MaxConcurrent-1 cannot both get in.
func (s *PostgresStore) Reserve(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("reserve run: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text))`, userID); err != nil {
		return uuid.Nil, fmt.Errorf("reserve run: lock: %w", err)
	}
	var running int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM generation_runs
		WHERE user_id = $1 AND status = 'running' AND started_at > NOW() - make_interval(secs => $2)`,
		userID, StaleAfter.Seconds()).Scan(&running); err != nil {
		return uuid.Nil, fmt.Errorf("reserve run: count: %w", err)
	}
	if running >= MaxConcurrent {
		return uuid.Nil, ErrAtCapacity
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO generation_runs (user_id) VALUES ($1) RETURNING id`, userID).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("reserve run: insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("reserve run: commit: %w", err)
	}
	return id, nil
}

// Release gives back a reservation the handler never used (quota refused,
// bad request). An attached row belongs to a real generation and is kept.
func (s *PostgresStore) Release(ctx context.Context, runID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM generation_runs WHERE id = $1 AND session_id IS NULL`, runID)
	if err != nil {
		return fmt.Errorf("release run: %w", err)
	}
	return nil
}

func (s *PostgresStore) Attach(ctx context.Context, runID, sessionID uuid.UUID, domain, city string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE generation_runs SET session_id = $2, domain = $3, city_name = $4
		WHERE id = $1`, runID, sessionID, domain, city)
	if err != nil {
		return fmt.Errorf("attach run: %w", err)
	}
	return nil
}

func (s *PostgresStore) Finish(ctx context.Context, runID uuid.UUID, status Status, errorCode string) (Run, bool, error) {
	run, err := scanRun(s.pool.QueryRow(ctx, `
		UPDATE generation_runs SET status = $2, error_code = $3, finished_at = NOW()
		WHERE id = $1 AND status = 'running'
		RETURNING `+runColumns, runID, string(status), errorCode))
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, fmt.Errorf("finish run: %w", err)
	}
	return run, true, nil
}

func (s *PostgresStore) Statuses(ctx context.Context, userID uuid.UUID, sessionIDs []uuid.UUID) ([]Run, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+runColumns+` FROM generation_runs
		WHERE user_id = $1 AND session_id = ANY($2)`, userID, sessionIDs)
	if err != nil {
		return nil, fmt.Errorf("run statuses: %w", err)
	}
	defer rows.Close()
	now := s.now()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("run statuses: scan: %w", err)
		}
		out = append(out, r.effective(now))
	}
	return out, rows.Err()
}

func (s *PostgresStore) FindBySession(ctx context.Context, userID, sessionID uuid.UUID) (Run, bool, error) {
	r, err := scanRun(s.pool.QueryRow(ctx, `
		SELECT `+runColumns+` FROM generation_runs WHERE user_id = $1 AND session_id = $2`, userID, sessionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, fmt.Errorf("find run: %w", err)
	}
	return r.effective(s.now()), true, nil
}

func (s *PostgresStore) ClaimNotification(ctx context.Context, runID uuid.UUID) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE generation_runs SET notified_at = NOW() WHERE id = $1 AND notified_at IS NULL`, runID)
	if err != nil {
		return false, fmt.Errorf("claim notification: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
```

- [ ] **Step 5: Run the tests**

Run: `RUNS_TEST_DSN=$LOCAL_DSN go test -tags=integration ./internal/domain/runs/ -v`
Expected: all 6 PASS. Also run `go vet ./internal/domain/runs/`.

- [ ] **Step 6: Commit**

```bash
git add pkg/db/migrations/NNNN_generation_runs.up.sql internal/domain/runs/runs.go internal/domain/runs/store.go internal/domain/runs/store_integration_test.go
git commit -m "Record each generation so a finished search has somewhere to be looked up"
```

### Task 3: One builder for a run's result URL

**Files:**
- Create: `internal/domain/runs/resultpath.go`
- Test: `internal/domain/runs/resultpath_test.go`
- Modify: `internal/domain/chat/service/chat_process_stream.go:757-800` (`sendCompletionEvent`)

**Interfaces:**
- Produces: `func ResultPath(domain string, sessionID uuid.UUID, cityName string, tripID uuid.UUID) (path, routeType string, query map[string]string)`

- [ ] **Step 1: Write the failing test**

```go
package runs

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestResultPath(t *testing.T) {
	sid := uuid.MustParse("3043fb3f-15e8-461f-86de-6426fb389df2")
	trip := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	cases := []struct {
		domain, want, route string
		trip                uuid.UUID
	}{
		{"itinerary", "/itinerary?sessionId=" + sid.String() + "&cityName=Crete&domain=itinerary", "itinerary", uuid.Nil},
		{"general", "/itinerary?sessionId=" + sid.String() + "&cityName=Crete&domain=itinerary", "itinerary", uuid.Nil},
		{"accommodation", "/hotels?sessionId=" + sid.String() + "&cityName=Crete&domain=hotels", "hotels", uuid.Nil},
		{"dining", "/restaurants?sessionId=" + sid.String() + "&cityName=Crete&domain=restaurants", "restaurants", uuid.Nil},
		{"activities", "/activities?sessionId=" + sid.String() + "&cityName=Crete&domain=activities", "activities", uuid.Nil},
		{"nearby", "/nearme?sessionId=" + sid.String() + "&cityName=Crete&domain=nearme", "nearme", uuid.Nil},
		{"itinerary", "/itinerary?sessionId=" + sid.String() + "&cityName=Crete&domain=itinerary&tripId=" + trip.String(), "itinerary", trip},
	}
	for _, c := range cases {
		got, route, _ := ResultPath(c.domain, sid, "Crete", c.trip)
		require.Equal(t, c.want, got, c.domain)
		require.Equal(t, c.route, route, c.domain)
	}
	got, _, _ := ResultPath("itinerary", sid, "Rio de Janeiro", uuid.Nil)
	require.Contains(t, got, "cityName=Rio+de+Janeiro")
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/domain/runs/ -run TestResultPath`
Expected: FAIL, `undefined: ResultPath`.

- [ ] **Step 3: Implement.** This is a move of the logic in `sendCompletionEvent`, not new behaviour:

```go
package runs

import (
	"fmt"
	"net/url"

	"github.com/google/uuid"
)

// ResultPath is the page that shows a run's result. The stream's complete
// event and the push notification both use it, so a tap and a redirect land
// in the same place.
func ResultPath(domain string, sessionID uuid.UUID, cityName string, tripID uuid.UUID) (path, routeType string, query map[string]string) {
	var base string
	switch domain {
	case "accommodation":
		routeType, base = "hotels", "/hotels"
	case "dining":
		routeType, base = "restaurants", "/restaurants"
	case "activities":
		routeType, base = "activities", "/activities"
	case "nearby":
		routeType, base = "nearme", "/nearme"
	default:
		routeType, base = "itinerary", "/itinerary"
	}
	query = map[string]string{
		"sessionId": sessionID.String(),
		"cityName":  cityName,
		"domain":    routeType,
	}
	path = fmt.Sprintf("%s?sessionId=%s&cityName=%s&domain=%s",
		base, sessionID.String(), url.QueryEscape(cityName), routeType)
	if tripID != uuid.Nil {
		query["tripId"] = tripID.String()
		path += "&tripId=" + tripID.String()
	}
	return path, routeType, query
}
```

In `sendCompletionEvent`, replace the `switch cc.Domain … navURL` block with:

```go
	navURL, routeType, queryParams := runs.ResultPath(string(cc.Domain), cc.SessionID, cc.CityName, cc.TripID)
```

Add the import `"github.com/FACorreiaa/loci-connect-api/internal/domain/runs"`. Keep the `l.sendEvent(... EventTypeComplete ...)` call as it is: it already uses `navURL`, `routeType` and `queryParams`. `Data` still reads `queryParams["tripId"]`, which is absent when there is no trip, exactly as before.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/domain/runs/ ./internal/domain/chat/... 2>&1 | tail -20`
Expected: PASS. If a chat service test asserts the old nav URL, it must still pass unchanged; the strings are identical.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/runs/resultpath.go internal/domain/runs/resultpath_test.go internal/domain/chat/service/chat_process_stream.go
git commit -m "Build a run's result URL in one place"
```

### Task 4: Cap interceptor that runs before quota

**Files:**
- Create: `internal/domain/runs/reservation.go`
- Create: `internal/domain/runs/cap_interceptor.go`
- Test: `internal/domain/runs/cap_interceptor_test.go`
- Modify: `cmd/api/router.go:158-181` (chain order)
- Modify: `cmd/api/dependencies.go` (construct the store once: `d.RunStore = runs.NewPostgresStore(d.DB.Pool)`; add `RunStore runs.Store` to `Dependencies`)

**Interfaces:**
- Consumes: `Store.Reserve`, `Store.Release`, `ErrAtCapacity` (Task 2).
- Produces:

```go
type Reservation struct { RunID uuid.UUID /* unexported: claimed atomic.Bool */ }
func (r *Reservation) Claim()                     // handler takes ownership; interceptor will not release
func ReservationFrom(ctx context.Context) (*Reservation, bool)
func NewCapInterceptor(store Store, logger *slog.Logger) *CapInterceptor // connect.Interceptor
const CapMessage = "You have 3 searches running — wait for one to finish"
```

- [ ] **Step 1: Write the failing tests** (fake store, real Connect handler over `httptest`):

```go
package runs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat/chatconnect"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

type fakeStore struct {
	Store
	mu        sync.Mutex
	full      bool
	reserved  []uuid.UUID
	released  []uuid.UUID
}

func (f *fakeStore) Reserve(context.Context, uuid.UUID) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.full {
		return uuid.Nil, ErrAtCapacity
	}
	id := uuid.New()
	f.reserved = append(f.reserved, id)
	return id, nil
}

func (f *fakeStore) Release(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, id)
	return nil
}

// withUser stands in for the auth interceptor, which runs before this one.
type withUser struct{ connect.Interceptor }

func (withUser) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		return next(context.WithValue(ctx, interceptors.UserIDKey, uuid.NewString()), conn)
	}
}
func (withUser) WrapUnary(n connect.UnaryFunc) connect.UnaryFunc { return n }
func (withUser) WrapStreamingClient(n connect.StreamingClientFunc) connect.StreamingClientFunc { return n }

func serve(t *testing.T, store Store, handler func(context.Context, *connect.Request[chatv1.ChatRequest], *connect.ServerStream[chatv1.StreamEvent]) error) chatconnect.ChatServiceClient {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(chatconnect.ChatServiceStreamChatProcedure, connect.NewServerStreamHandler(
		chatconnect.ChatServiceStreamChatProcedure, handler,
		connect.WithInterceptors(withUser{}, NewCapInterceptor(store, nil)),
	))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return chatconnect.NewChatServiceClient(srv.Client(), srv.URL)
}

func drain(t *testing.T, c chatconnect.ChatServiceClient, req *chatv1.ChatRequest) error {
	t.Helper()
	s, err := c.StreamChat(context.Background(), connect.NewRequest(req))
	require.NoError(t, err)
	for s.Receive() {
	}
	return s.Err()
}

func TestCapRefusesBeforeTheHandlerRuns(t *testing.T) {
	store := &fakeStore{full: true}
	ran := false
	c := serve(t, store, func(context.Context, *connect.Request[chatv1.ChatRequest], *connect.ServerStream[chatv1.StreamEvent]) error {
		ran = true
		return nil
	})
	err := drain(t, c, &chatv1.ChatRequest{Message: "3 days in Crete"})
	require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
	require.Contains(t, err.Error(), CapMessage)
	require.False(t, ran)
}

func TestResumeIsNotCapped(t *testing.T) {
	store := &fakeStore{full: true}
	c := serve(t, store, func(context.Context, *connect.Request[chatv1.ChatRequest], *connect.ServerStream[chatv1.StreamEvent]) error {
		return nil
	})
	token := "evt-1"
	sid := uuid.NewString()
	require.NoError(t, drain(t, c, &chatv1.ChatRequest{Message: "x", SessionId: &sid, ResumeToken: &token}))
	require.Empty(t, store.reserved)
}

func TestHandlerSeesTheRequestAndTheReservation(t *testing.T) {
	store := &fakeStore{}
	var got string
	var claimed bool
	c := serve(t, store, func(ctx context.Context, req *connect.Request[chatv1.ChatRequest], _ *connect.ServerStream[chatv1.StreamEvent]) error {
		got = req.Msg.GetMessage()
		r, ok := ReservationFrom(ctx)
		claimed = ok
		r.Claim()
		return nil
	})
	require.NoError(t, drain(t, c, &chatv1.ChatRequest{Message: "3 days in Crete"}))
	require.Equal(t, "3 days in Crete", got, "the peeked message must reach the handler intact")
	require.True(t, claimed)
	require.Empty(t, store.released, "a claimed reservation belongs to the handler")
}

func TestUnclaimedReservationIsReleased(t *testing.T) {
	store := &fakeStore{}
	c := serve(t, store, func(context.Context, *connect.Request[chatv1.ChatRequest], *connect.ServerStream[chatv1.StreamEvent]) error {
		return connect.NewError(connect.CodeResourceExhausted, errors.New("daily quota"))
	})
	_ = drain(t, c, &chatv1.ChatRequest{Message: "x"})
	require.Equal(t, store.reserved, store.released)
}
```

`ChatRequest.session_id` / `resume_token` are `optional` in the proto, hence `&sid` / `&token`. If `session_id` turns out to be a plain `string`, pass `sid` directly.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/domain/runs/ -run 'Cap|Resume|Reservation|Unclaimed'`
Expected: FAIL, `undefined: NewCapInterceptor`.

- [ ] **Step 3: Implement `reservation.go`**

```go
package runs

import (
	"context"
	"sync/atomic"

	"github.com/google/uuid"
)

// Reservation is a run row the cap interceptor inserted for this request.
// The handler claims it once it has a generation to attach it to; an
// unclaimed one is released when the request ends, so quota refusals and
// bad requests do not leave phantom running rows behind.
type Reservation struct {
	RunID   uuid.UUID
	claimed atomic.Bool
}

func (r *Reservation) Claim() { r.claimed.Store(true) }

type reservationKey struct{}

func withReservation(ctx context.Context, r *Reservation) context.Context {
	return context.WithValue(ctx, reservationKey{}, r)
}

func ReservationFrom(ctx context.Context) (*Reservation, bool) {
	r, ok := ctx.Value(reservationKey{}).(*Reservation)
	return r, ok
}
```

- [ ] **Step 4: Implement `cap_interceptor.go`**

```go
package runs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat/chatconnect"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

const CapMessage = "You have 3 searches running — wait for one to finish"

// CapInterceptor holds each person to MaxConcurrent generations. It sits
// after auth (it needs the user id) and before the quota interceptor, so a
// refused search costs no quota. It reads the first request itself to tell
// a resume (never capped) from a new search, then hands that same message
// to the handler.
type CapInterceptor struct {
	store  Store
	logger *slog.Logger
}

func NewCapInterceptor(store Store, logger *slog.Logger) *CapInterceptor {
	if logger == nil {
		logger = slog.Default()
	}
	return &CapInterceptor{store: store, logger: logger}
}

func (i *CapInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc { return next }

func (i *CapInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *CapInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if conn.Spec().Procedure != chatconnect.ChatServiceStreamChatProcedure {
			return next(ctx, conn)
		}
		userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
		userID, err := uuid.Parse(userIDStr)
		if !ok || err != nil {
			return next(ctx, conn) // the handler answers Unauthenticated
		}

		first := &chatv1.ChatRequest{}
		if err := conn.Receive(first); err != nil {
			return err
		}
		peeked := &peekedConn{StreamingHandlerConn: conn, first: first}
		if first.GetResumeToken() != "" {
			return next(ctx, peeked)
		}

		runID, err := i.store.Reserve(ctx, userID)
		if errors.Is(err, ErrAtCapacity) {
			return connect.NewError(connect.CodeResourceExhausted, errors.New(CapMessage))
		}
		if err != nil {
			// Losing the record must not lose the search: log and serve it
			// uncapped rather than fail someone's request on a DB hiccup.
			i.logger.Error("run reservation failed; serving uncapped", "error", err)
			return next(ctx, peeked)
		}

		res := &Reservation{RunID: runID}
		defer func() {
			if !res.claimed.Load() {
				if rErr := i.store.Release(context.WithoutCancel(ctx), runID); rErr != nil {
					i.logger.Warn("release unclaimed run", "run_id", runID, "error", rErr)
				}
			}
		}()
		return next(withReservation(ctx, res), peeked)
	}
}

// peekedConn returns the already-read first request, then defers to the
// real connection.
type peekedConn struct {
	connect.StreamingHandlerConn
	first *chatv1.ChatRequest
	used  bool
}

func (c *peekedConn) Receive(msg any) error {
	if c.used {
		return c.StreamingHandlerConn.Receive(msg)
	}
	c.used = true
	dst, ok := msg.(proto.Message)
	if !ok {
		return fmt.Errorf("runs: unexpected request type %T", msg)
	}
	proto.Merge(dst, c.first)
	return nil
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/domain/runs/ -v -run 'Cap|Resume|Reservation|Unclaimed'`
Expected: 4 PASS.

- [ ] **Step 6: Wire it in.**
  - In `cmd/api/dependencies.go`, next to the other DB-backed services, add `d.RunStore = runs.NewPostgresStore(d.DB.Pool)`, and add the field `RunStore runs.Store` to `Dependencies`.
  - In `cmd/api/router.go`, build `runCapInterceptor := runs.NewCapInterceptor(deps.RunStore, deps.Logger)` and put it in the chain **between `userRateLimiter` and `subscriptionInterceptor`**:

```go
		authInterceptor,
		userRateLimiter,
		runCapInterceptor, // after auth (needs the user), before quota (a refusal costs nothing)
		subscriptionInterceptor,
```

- [ ] **Step 7: Guard the order with a test.** Add to `cmd/api/interceptor_chain_test.go` a test that builds the StreamChat handler with `interceptors.NewAuthInterceptor(secret)`, `runs.NewCapInterceptor(fullStore, nil)` and `subscription.NewRateLimitInterceptor(subSvc)` in that order. Use a store whose `Reserve` returns `runs.ErrAtCapacity`, and the file's existing `recordingSubscriptionService` and token helper, the same way `TestInterceptorChain_StreamChatConsumesQuota` does. Assert `connect.CodeResourceExhausted` and `subSvc.consumeCalled == false`.

Run: `go test ./cmd/api/ -run InterceptorChain -v`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/domain/runs/reservation.go internal/domain/runs/cap_interceptor.go internal/domain/runs/cap_interceptor_test.go cmd/api/router.go cmd/api/dependencies.go cmd/api/interceptor_chain_test.go
git commit -m "Hold each person to three searches at once, refused before quota is spent"
```

### Task 5: Tracker — stream events become run status

**Files:**
- Create: `internal/domain/runs/tracker.go`
- Test: `internal/domain/runs/tracker_test.go`
- Modify: `internal/domain/chat/handler/chat_handler.go` (`ChatHandler` struct ~33, `NewChatHandler` ~47, `StreamChat` 97-285)
- Modify: `cmd/api/dependencies.go:732` (pass the store)

**Interfaces:**
- Consumes: `Store.Attach`, `Store.Finish`, `Reservation` (Tasks 2, 4).
- Produces:

```go
type FinishListener func(ctx context.Context, run Run)
func NewTracker(store Store, runID uuid.UUID, onFinish FinishListener, logger *slog.Logger) *Tracker
func (t *Tracker) Observe(ev locitypes.StreamEvent)   // safe from the live loop and the drain goroutine
func (t *Tracker) Close()                              // stream ended; no terminal event → failed "incomplete"
// ChatHandler gains: func (h *ChatHandler) WithRuns(store runs.Store, onFinish runs.FinishListener) *ChatHandler
```

- [ ] **Step 1: Write the failing test**

```go
package runs

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

type recordingStore struct {
	Store
	mu       sync.Mutex
	attached []string
	finished []Status
	codes    []string
}

func (r *recordingStore) Attach(_ context.Context, _, sid uuid.UUID, domain, city string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attached = append(r.attached, sid.String()+"|"+domain+"|"+city)
	return nil
}

func (r *recordingStore) Finish(_ context.Context, id uuid.UUID, s Status, code string) (Run, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finished = append(r.finished, s)
	r.codes = append(r.codes, code)
	return Run{ID: id, Status: s}, true, nil
}

func start(sid string) locitypes.StreamEvent {
	return locitypes.StreamEvent{Type: locitypes.EventTypeStart, Data: locitypes.StreamStartData{SessionID: sid, Domain: "itinerary", City: "Crete"}}
}

func TestTrackerAttachesThenFinishesDone(t *testing.T) {
	store := &recordingStore{}
	var got []Run
	tr := NewTracker(store, uuid.New(), func(_ context.Context, r Run) { got = append(got, r) }, nil)
	sid := uuid.NewString()

	tr.Observe(start(sid))
	tr.Observe(locitypes.StreamEvent{Type: "token"})
	tr.Observe(locitypes.StreamEvent{Type: locitypes.EventTypeComplete})
	tr.Close()

	require.Equal(t, []string{sid + "|itinerary|Crete"}, store.attached)
	require.Equal(t, []Status{StatusDone}, store.finished, "Close after complete must not write again")
	require.Len(t, got, 1)
}

func TestTrackerErrorFinishesFailedWithCode(t *testing.T) {
	store := &recordingStore{}
	tr := NewTracker(store, uuid.New(), nil, nil)
	tr.Observe(start(uuid.NewString()))
	tr.Observe(locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: "boom", ErrorCode: "unavailable"})
	require.Equal(t, []Status{StatusFailed}, store.finished)
	require.Equal(t, []string{"unavailable"}, store.codes)
}

func TestTrackerStreamEndingSilentlyIsAFailure(t *testing.T) {
	store := &recordingStore{}
	tr := NewTracker(store, uuid.New(), nil, nil)
	tr.Observe(start(uuid.NewString()))
	tr.Close()
	require.Equal(t, []Status{StatusFailed}, store.finished)
	require.Equal(t, []string{"incomplete"}, store.codes)
}

func TestNilTrackerIsANoop(t *testing.T) {
	var tr *Tracker
	tr.Observe(start(uuid.NewString()))
	tr.Close()
}
```

Before writing this, check that `locitypes.StreamEvent` has an `ErrorCode string` field (types/chat_session.go:191 documents one). If its type is not `string`, convert it with `string(...)` in `Observe`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/domain/runs/ -run Tracker`
Expected: FAIL, `undefined: NewTracker`.

- [ ] **Step 3: Implement `tracker.go`**

```go
package runs

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// FinishListener hears about a run the moment it stops running. Push
// notification is the only listener; it must not block.
type FinishListener func(ctx context.Context, run Run)

// Tracker turns one stream's events into writes on its run row. The
// handler feeds it from the live loop and, after a disconnect, from the
// drain goroutine, so the row finishes whether or not anybody is watching.
type Tracker struct {
	store    Store
	runID    uuid.UUID
	onFinish FinishListener
	logger   *slog.Logger

	mu       sync.Mutex
	attached bool
	finished bool
}

func NewTracker(store Store, runID uuid.UUID, onFinish FinishListener, logger *slog.Logger) *Tracker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Tracker{store: store, runID: runID, onFinish: onFinish, logger: logger}
}

// writeCtx outlives the RPC: the row must be written after the client left.
func writeCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

func (t *Tracker) Observe(ev locitypes.StreamEvent) {
	if t == nil {
		return
	}
	switch ev.Type {
	case locitypes.EventTypeStart:
		t.attach(ev)
	case locitypes.EventTypeComplete:
		t.finish(StatusDone, "")
	case locitypes.EventTypeError:
		code := ev.ErrorCode
		if code == "" {
			code = "internal"
		}
		t.finish(StatusFailed, code)
	}
}

func (t *Tracker) Close() {
	if t == nil {
		return
	}
	t.finish(StatusFailed, "incomplete")
}

func (t *Tracker) attach(ev locitypes.StreamEvent) {
	var sd locitypes.StreamStartData
	switch d := ev.Data.(type) {
	case locitypes.StreamStartData:
		sd = d
	case *locitypes.StreamStartData:
		sd = *d
	default:
		raw, _ := json.Marshal(ev.Data)
		_ = json.Unmarshal(raw, &sd)
	}
	sid, err := uuid.Parse(sd.SessionID)
	if err != nil {
		return
	}
	t.mu.Lock()
	if t.attached {
		t.mu.Unlock()
		return
	}
	t.attached = true
	t.mu.Unlock()

	ctx, cancel := writeCtx()
	defer cancel()
	if err := t.store.Attach(ctx, t.runID, sid, sd.Domain, sd.City); err != nil {
		t.logger.Warn("attach run", "run_id", t.runID, "error", err)
	}
}

func (t *Tracker) finish(status Status, code string) {
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		return
	}
	t.finished = true
	t.mu.Unlock()

	ctx, cancel := writeCtx()
	defer cancel()
	run, moved, err := t.store.Finish(ctx, t.runID, status, code)
	if err != nil {
		t.logger.Warn("finish run", "run_id", t.runID, "status", status, "error", err)
		return
	}
	if moved && t.onFinish != nil {
		t.onFinish(context.WithoutCancel(ctx), run)
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/domain/runs/ -run Tracker -v`
Expected: 4 PASS.

- [ ] **Step 5: Wire it into `StreamChat`.** In `chat_handler.go`:

1. Add fields to `ChatHandler`: `runs runs.Store` and `onRunFinish runs.FinishListener`. Add the method:

```go
// WithRuns records each generation and announces its end to onFinish.
func (h *ChatHandler) WithRuns(store runs.Store, onFinish runs.FinishListener) *ChatHandler {
	h.runs = store
	h.onRunFinish = onFinish
	return h
}
```

2. In `StreamChat`, just before `go func() { defer func() { llmCancel(); close(eventCh) }()`, after the resume block, add:

```go
	var tracker *runs.Tracker
	if res, ok := runs.ReservationFrom(ctx); ok && h.runs != nil {
		res.Claim()
		tracker = runs.NewTracker(h.runs, res.RunID, h.onRunFinish, h.logger)
	}
```

3. Make `appendEvent` also observe: rename it to `record` and, as its **first** line, call `tracker.Observe(ev)`. Then keep the existing buffer logic. Update the three call sites: the live loop and the two drain goroutines.

4. Close the tracker when the event channel closes, on every path:
   - In the live loop, in the `if !ok {` branch, before `return nil`, add `tracker.Close()`.
   - In both drain goroutines, change `for ev := range eventCh { appendEvent(ev) }` to:

```go
				go func() {
					for ev := range eventCh {
						record(ev)
					}
					tracker.Close()
				}()
```

   - In the branch that returns right after a `Complete`/`Error` event (`if event.Type == ... { return nil }`), the pipeline goroutine still closes `eventCh` afterwards. Replace that `return nil` with a drain so later events keep being recorded:

```go
			if event.Type == locitypes.EventTypeComplete || event.Type == locitypes.EventTypeError {
				h.logger.Info("Stream completed", "event_type", event.Type)
				go func() {
					for ev := range eventCh {
						record(ev)
					}
					tracker.Close()
				}()
				return nil
			}
```

`Close` after a terminal event is a no-op (the `finished` guard), so the silent-end failure is only written when no terminal event ever came.

5. In `cmd/api/dependencies.go:732`, chain `.WithRuns(d.RunStore, nil)` onto `NewChatHandler(...)`. Task 10 replaces `nil` with the notifier.

- [ ] **Step 6: Build and run the chat tests**

Run: `go build ./... && go test ./internal/domain/chat/... ./internal/domain/runs/ ./cmd/api/`
Expected: PASS. Existing handler tests construct `ChatHandler` without `WithRuns`: `tracker` stays nil and every call is a no-op.

- [ ] **Step 7: Commit**

```bash
git add internal/domain/runs/tracker.go internal/domain/runs/tracker_test.go internal/domain/chat/handler/chat_handler.go cmd/api/dependencies.go
git commit -m "Mark a run done or failed from its own stream, even after the client left"
```

### Task 6: Resume follows a live run; a finished run with no buffer says where to look

**Files:**
- Modify: `internal/domain/chat/resumebuf/resumebuf.go`
- Test: `internal/domain/chat/resumebuf/resumebuf_test.go` (extend; create if absent)
- Modify: `internal/domain/chat/handler/chat_handler.go:175-195` (resume block)
- Test: `internal/domain/chat/handler/chat_resume_test.go` (new)

**Interfaces:**
- Produces: `func (b *Buffer) Subscribe(sessionID, afterEventID string) (backlog []locitypes.StreamEvent, live <-chan locitypes.StreamEvent, cancel func(), ok bool)`. `live` is closed after a terminal event (complete/error) is appended, or on `cancel`. `ok == false` means an unknown session.

- [ ] **Step 1: Write failing buffer tests**

```go
package resumebuf

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func TestSubscribeReplaysThenFollowsUntilComplete(t *testing.T) {
	b := New()
	b.Append("s1", locitypes.StreamEvent{Type: "start", EventID: "e1"})
	b.Append("s1", locitypes.StreamEvent{Type: "token", EventID: "e2"})

	backlog, live, cancel, ok := b.Subscribe("s1", "e1")
	defer cancel()
	require.True(t, ok)
	require.Len(t, backlog, 1)
	require.Equal(t, "e2", backlog[0].EventID)

	b.Append("s1", locitypes.StreamEvent{Type: "itinerary", EventID: "e3"})
	b.Append("s1", locitypes.StreamEvent{Type: locitypes.EventTypeComplete, EventID: "e4"})

	var got []string
	for ev := range live {
		got = append(got, ev.EventID)
	}
	require.Equal(t, []string{"e3", "e4"}, got)
}

func TestSubscribeToFinishedRunClosesImmediately(t *testing.T) {
	b := New()
	b.Append("s1", locitypes.StreamEvent{Type: locitypes.EventTypeComplete, EventID: "e1"})
	backlog, live, cancel, ok := b.Subscribe("s1", "")
	defer cancel()
	require.True(t, ok)
	require.Len(t, backlog, 1)
	select {
	case _, open := <-live:
		require.False(t, open)
	case <-time.After(time.Second):
		t.Fatal("live channel of a finished run must be closed")
	}
}

func TestSubscribeUnknownSession(t *testing.T) {
	_, _, _, ok := New().Subscribe("nope", "")
	require.False(t, ok)
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/domain/chat/resumebuf/ -run Subscribe`
Expected: FAIL, `b.Subscribe undefined`.

- [ ] **Step 3: Implement.** In `resumebuf.go`, add to `sessionBuf`: `done bool` and `subs map[chan locitypes.StreamEvent]struct{}`. In `Append`, after appending (still under the lock):

```go
	for ch := range s.subs {
		select {
		case ch <- ev:
		default: // a subscriber too slow for a 64-event buffer re-syncs on its next resume
		}
	}
	if ev.Type == locitypes.EventTypeComplete || ev.Type == locitypes.EventTypeError {
		s.done = true
		for ch := range s.subs {
			close(ch)
		}
		s.subs = nil
	}
```

Add:

```go
// Subscribe is Replay that keeps going: the backlog after afterEventID, then
// every event appended until the run's terminal event, when live closes.
func (b *Buffer) Subscribe(sessionID, afterEventID string) (backlog []locitypes.StreamEvent, live <-chan locitypes.StreamEvent, cancel func(), ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.sessions[sessionID]
	if s == nil {
		return nil, nil, func() {}, false
	}
	s.lastAccess = b.now()
	start := 0
	if afterEventID != "" {
		for i, ev := range s.events {
			if ev.EventID == afterEventID {
				start = i + 1
				break
			}
		}
	}
	backlog = append([]locitypes.StreamEvent(nil), s.events[start:]...)

	ch := make(chan locitypes.StreamEvent, 64)
	if s.done {
		close(ch)
		return backlog, ch, func() {}, true
	}
	if s.subs == nil {
		s.subs = make(map[chan locitypes.StreamEvent]struct{})
	}
	s.subs[ch] = struct{}{}
	cancel = func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, still := s.subs[ch]; still {
			delete(s.subs, ch)
			close(ch)
		}
	}
	return backlog, ch, cancel, true
}
```

The reaper (`reapLocked`) must close the subscribers of a session it evicts: before `delete(b.sessions, id)`, loop over `s.subs`, close each channel, and set `s.subs = nil`.

- [ ] **Step 4: Run buffer tests**

Run: `go test ./internal/domain/chat/resumebuf/ -v -race`
Expected: PASS, no race reports.

- [ ] **Step 5: Use it in the handler.** Replace the resume block in `StreamChat` (`if h.resumeBuf != nil && cc.ResumeToken != "" && bufSessionID != "" { … }`) with:

```go
	if cc.ResumeToken != "" && bufSessionID != "" {
		if h.resumeBuf != nil {
			if backlog, live, cancel, found := h.resumeBuf.Subscribe(bufSessionID, cc.ResumeToken); found {
				llmCancel()
				defer cancel()
				send := func(ev locitypes.StreamEvent) bool {
					resp, mErr := h.mapEventToProto(ctx, ev, userID)
					if mErr != nil {
						return true
					}
					return stream.Send(resp) == nil
				}
				for _, ev := range backlog {
					if !send(ev) {
						return nil
					}
				}
				for {
					select {
					case ev, open := <-live:
						if !open {
							h.logger.Info("resumed stream to its end", "session_id", bufSessionID)
							return nil
						}
						if !send(ev) {
							return nil
						}
					case <-ctx.Done():
						return nil
					}
				}
			}
		}
		// No buffer (evicted, or another pod). If this session's run already
		// ended, point the client at the stored result instead of generating
		// the whole answer again.
		if h.runs != nil {
			if run, found, rErr := h.runs.FindBySession(ctx, userID, requestedSessionID); rErr == nil && found && run.Status != runs.StatusRunning {
				llmCancel()
				return h.sendLoadFromSession(stream, run)
			}
		}
	}
```

Add to the handler:

```go
// sendLoadFromSession ends a resume whose events are gone: the run finished,
// so its result is stored and GetChatSession / GetSessionPOIs can load it.
func (h *ChatHandler) sendLoadFromSession(stream *connect.ServerStream[chatv1.StreamEvent], run runs.Run) error {
	if run.Status == runs.StatusFailed {
		return stream.Send(&chatv1.StreamEvent{
			Timestamp: timestamppb.Now(),
			EventId:   uuid.NewString(),
			IsFinal:   true,
			EventType: chatv1.StreamEventType_STREAM_EVENT_TYPE_ERROR,
			Payload: &chatv1.StreamEvent_Error{Error: &chatv1.StreamError{
				UserMessage:  "This search didn't finish. Try it again.",
				InternalCode: run.ErrorCode,
				Retryable:    true,
			}},
		})
	}
	path, routeType, query := runs.ResultPath(run.Domain, run.SessionID, run.CityName, uuid.Nil)
	return stream.Send(&chatv1.StreamEvent{
		Timestamp:  timestamppb.Now(),
		EventId:    uuid.NewString(),
		IsFinal:    true,
		EventType:  chatv1.StreamEventType_STREAM_EVENT_TYPE_COMPLETE,
		Navigation: &chatv1.NavigationData{Url: path, RouteType: routeType, QueryParams: query},
		Payload: &chatv1.StreamEvent_Complete{Complete: &chatv1.CompletePayload{
			SessionId:       run.SessionID.String(),
			LoadFromSession: true,
		}},
	})
}
```

Before relying on `NavigationData.Url`: it is validated as `uri: true`. Check how `mapEventToProto` fills `Navigation` for the normal complete event today, and do exactly the same. If the existing code prefixes a base URL to satisfy `uri`, reuse that helper. The `protovalidate` interceptor only validates requests, but match the existing shape anyway.

When `ErrorCode` is empty, `InternalCode` has `min_len: 1`, so use `"internal"` as the fallback.

- [ ] **Step 6: Handler tests** in `internal/domain/chat/handler/chat_resume_test.go`:
  - **Follow live:**
    - Build a `ChatHandler` with a fake `LlmInteractiontService` whose `ProcessUnifiedChatMessageStream` is never called. Look at how existing `chat_handler` tests build one, and copy that.
    - Use `h.resumeBuf = resumebuf.New()`, pre-appended with `start(e1)` and `token(e2)`.
    - Serve StreamChat over `httptest` with a context that carries `interceptors.UserIDKey`, using the `withUser` pattern from Task 4.
    - Start a resume with `ResumeToken: "e1"`. In a goroutine after 50 ms, append `complete(e3)`.
    - Assert the client receives `e2` then a COMPLETE and the stream ends.
  - **Buffer gone, run done:**
    - Leave the buffer empty, and give the handler `WithRuns(fakeStore)` where `FindBySession` returns `Run{Status: StatusDone, SessionID: sid, Domain: "itinerary", CityName: "Crete"}`.
    - Assert a single COMPLETE event with `LoadFromSession == true` and no call to the service.
  - **Buffer gone, run failed:** assert a single ERROR event with `Retryable == true`.

Run: `go test ./internal/domain/chat/handler/ -run Resume -v -race`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/domain/chat/resumebuf/ internal/domain/chat/handler/chat_handler.go internal/domain/chat/handler/chat_resume_test.go
git commit -m "Resume follows a search that is still running, and points at the stored result once it has ended"
```

### Task 7: `GetRunStatus`

**Files:**
- Create: `internal/domain/runs/handler.go`
- Test: `internal/domain/runs/handler_test.go`
- Modify: `internal/domain/chat/handler/chat_handler.go` (delegate method)

**Interfaces:**
- Consumes: `Store.Statuses` (Task 2), `ResultPath` (Task 3).
- Produces: `func StatusesToProto(runs []Run) []*chatv1.RunInfo` and `ChatHandler.GetRunStatus`.

- [ ] **Step 1: Write the failing test**

```go
package runs

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
)

func TestStatusesToProto(t *testing.T) {
	sid := uuid.New()
	done := time.Now()
	got := StatusesToProto([]Run{
		{SessionID: sid, Domain: "accommodation", CityName: "Crete", Status: StatusDone, FinishedAt: &done},
		{SessionID: uuid.Nil, Status: StatusRunning}, // never attached: not reportable
	})
	require.Len(t, got, 1)
	require.Equal(t, sid.String(), got[0].GetSessionId())
	require.Equal(t, chatv1.RunStatus_RUN_STATUS_DONE, got[0].GetStatus())
	require.Equal(t, chatv1.DomainType_DOMAIN_TYPE_ACCOMMODATION, got[0].GetDomain())
	require.Equal(t, "/hotels?sessionId="+sid.String()+"&cityName=Crete&domain=hotels", got[0].GetUrl())
	require.NotNil(t, got[0].GetFinishedAt())
}
```

Check the generated `DomainType` constant names in `gen/go/loci/chat/chat.pb.go` (grep `DomainType_`) and use the real ones. There may already be a `string → DomainType` mapper in `internal/domain/chat/presenter`; grep for `DomainType_DOMAIN_TYPE_` and reuse it rather than writing a second one.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/domain/runs/ -run StatusesToProto`
Expected: FAIL, undefined.

- [ ] **Step 3: Implement `handler.go`**

```go
package runs

import (
	"google.golang.org/protobuf/types/known/timestamppb"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"

	"github.com/google/uuid"
)

var protoStatus = map[Status]chatv1.RunStatus{
	StatusRunning: chatv1.RunStatus_RUN_STATUS_RUNNING,
	StatusDone:    chatv1.RunStatus_RUN_STATUS_DONE,
	StatusFailed:  chatv1.RunStatus_RUN_STATUS_FAILED,
}

// StatusesToProto reports runs the client can act on: ones with a session.
func StatusesToProto(runs []Run) []*chatv1.RunInfo {
	out := make([]*chatv1.RunInfo, 0, len(runs))
	for _, r := range runs {
		if r.SessionID == uuid.Nil {
			continue
		}
		path, _, _ := ResultPath(r.Domain, r.SessionID, r.CityName, uuid.Nil)
		info := &chatv1.RunInfo{
			SessionId: r.SessionID.String(),
			Domain:    domainToProto(r.Domain),
			CityName:  r.CityName,
			Status:    protoStatus[r.Status],
			ErrorCode: r.ErrorCode,
			Url:       path,
		}
		if r.FinishedAt != nil {
			info.FinishedAt = timestamppb.New(*r.FinishedAt)
		}
		out = append(out, info)
	}
	return out
}
```

`domainToProto` is the existing presenter mapper if there is one (import it). Otherwise define it here: a `switch` over `"itinerary"`, `"general"`, `"accommodation"`, `"dining"`, `"activities"` and `"nearby"`, returning the generated constants, with a default of `UNSPECIFIED`.

In `chat_handler.go`:

```go
// GetRunStatus reports the caller's runs among session_ids.
func (h *ChatHandler) GetRunStatus(
	ctx context.Context,
	req *connect.Request[chatv1.GetRunStatusRequest],
) (*connect.Response[chatv1.GetRunStatusResponse], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	userID, err := uuid.Parse(userIDStr)
	if !ok || err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	if h.runs == nil {
		return connect.NewResponse(&chatv1.GetRunStatusResponse{}), nil
	}
	ids := make([]uuid.UUID, 0, len(req.Msg.GetSessionIds()))
	for _, s := range req.Msg.GetSessionIds() {
		if id, pErr := uuid.Parse(s); pErr == nil {
			ids = append(ids, id)
		}
	}
	found, err := h.runs.Statuses(ctx, userID, ids)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&chatv1.GetRunStatusResponse{Runs: runs.StatusesToProto(found)}), nil
}
```

- [ ] **Step 4: Run tests and build**

Run: `go test ./internal/domain/runs/ ./internal/domain/chat/... && go build ./...`
Expected: PASS. `go build` fails if the generated `ChatServiceHandler` interface now requires `GetRunStatus` and something else implements it; add the method there too, returning `CodeUnimplemented`.

- [ ] **Step 5: Lint, then open PR A**

```bash
golangci-lint run ./...        # v2.13.2; v2.13.0's staticcheck hangs
git add internal/domain/runs/handler.go internal/domain/runs/handler_test.go internal/domain/chat/handler/chat_handler.go
git commit -m "Let a client ask which of its searches finished"
git push -u origin HEAD
gh pr create --title "Record searches, cap them at three, and resume one still running" --body "Implements Phase 1 of docs/superpowers/plans/2026-09-22-background-run-notifications.md"
```

Before merging, set the migration number to the next free one on `origin/main`.

---

## Phase 2 — Server PR B: push delivery

### Task 8: `push_devices`, `search_finished`, and the settings plumbing

**Files:**
- Create: `pkg/db/migrations/MMMM_push_devices.up.sql`
- Modify: `internal/types/profiles.go:367-378` (settings types)
- Modify: `internal/domain/user/user_repository.go:617-660`
- Modify: `internal/domain/user/handler/user_handler.go:368-420`
- Test: `internal/domain/user/user_integration_test.go` (extend)

**Interfaces:**
- Produces: `NotificationSettings.SearchFinished bool`, `UpdateNotificationSettingsParams.SearchFinished *bool`.

- [ ] **Step 1: Migration**

```sql
-- +goose Up
-- +goose StatementBegin
-- Where to deliver a push. A browser's web-push subscription (or, later, a
-- phone's APNs token) belongs to one account at a time; re-registering an
-- endpoint moves it to whoever is signed in now.
CREATE TABLE IF NOT EXISTS push_devices (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    platform     TEXT NOT NULL CHECK (platform IN ('web_push', 'apns')),
    endpoint     TEXT NOT NULL UNIQUE,
    p256dh       TEXT NOT NULL DEFAULT '',
    auth         TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS push_devices_user_idx ON push_devices (user_id);

-- The first switch that actually sends something. On by default: the
-- browser's own permission prompt, asked in context, is the consent.
ALTER TABLE notification_settings
    ADD COLUMN IF NOT EXISTS search_finished BOOLEAN NOT NULL DEFAULT TRUE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE notification_settings DROP COLUMN IF EXISTS search_finished;
DROP TABLE IF EXISTS push_devices;
-- +goose StatementEnd
```

- [ ] **Step 2: Failing test.** Following the existing user integration test setup, add a test: update with `SearchFinished: ptr(false)` only, assert `SearchFinished == false` and that `Recommendations`/`TripReminders` are unchanged; then a fresh user's `GetNotificationSettings` returns `SearchFinished == true`.

Run: `<the DSN env that file uses>=… go test -tags=integration ./internal/domain/user/ -run NotificationSettings`
Expected: compile failure (`SearchFinished` undefined).

- [ ] **Step 3: Implement.**
  - Add `SearchFinished bool \`json:"search_finished"\`` to `NotificationSettings` and `SearchFinished *bool \`json:"search_finished,omitempty"\`` to the params.
  - In both repository queries add the column: `RETURNING recommendations, trip_reminders, search_finished, updated_at`, and scan into `&out.SearchFinished`.
  - In the update insert, `VALUES ($1, COALESCE($2, FALSE), COALESCE($3, FALSE), COALESCE($4, TRUE), NOW())` with `search_finished = COALESCE($4, notification_settings.search_finished)`, passing `params.SearchFinished` as `$4`.
  - In the handler, pass `SearchFinished: req.Msg.SearchFinished`; in `toProtoNotificationSettings` set `SearchFinished: s.SearchFinished`.
  - Also update the comments that say delivery does not exist. The migration 0092 comment stays (history); the handler and repo comments now say that `search_finished` is delivered as a push.

- [ ] **Step 4: Run tests**

Run: the integration test above plus `go test ./internal/domain/user/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/db/migrations/MMMM_push_devices.up.sql internal/types/profiles.go internal/domain/user/
git commit -m "Store push devices and the search-finished switch"
```

### Task 9: Device registration RPCs, `GetPushConfig`, and config

**Files:**
- Create: `internal/domain/push/devices.go`
- Test: `internal/domain/push/devices_integration_test.go`
- Modify: `pkg/config/config.go` (add `Push PushConfig` to `Config`; load in the same constructor that loads `Stripe`, ~line 393)
- Modify: `internal/domain/user/handler/user_handler.go` (3 RPCs), `cmd/api/dependencies.go`

**Interfaces:**
- Produces:

```go
package push
type Device struct { ID, UserID uuid.UUID; Platform, Endpoint, P256dh, Auth string }
type DeviceStore interface {
	Upsert(ctx context.Context, userID uuid.UUID, platform, endpoint, p256dh, auth, userAgent string) error
	Remove(ctx context.Context, userID uuid.UUID, endpoint string) error
	RemoveEndpoint(ctx context.Context, endpoint string) error     // push service said 404/410
	ForUser(ctx context.Context, userID uuid.UUID, platform string) ([]Device, error)
}
func NewPostgresDeviceStore(pool *pgxpool.Pool) *PostgresDeviceStore
// config
type PushConfig struct { VAPIDPublicKey, VAPIDPrivateKey, VAPIDSubject string }
func (c PushConfig) Enabled() bool // all three non-empty
// user handler
func (h *UserHandler) WithPush(devices push.DeviceStore, cfg config.PushConfig) *UserHandler
```

- [ ] **Step 1: Failing integration test** (`PUSH_TEST_DSN`, same harness shape as Task 2):
  - `Upsert` twice with the same endpoint for user A, then once for user B: `ForUser(A)` is empty, `ForUser(B)` has 1.
  - `Remove(B, endpoint)` empties it.
  - `RemoveEndpoint` removes regardless of user.

Run: `PUSH_TEST_DSN=$LOCAL_DSN go test -tags=integration ./internal/domain/push/`
Expected: compile failure.

- [ ] **Step 2: Implement `devices.go`**

```go
// Package push delivers "your search is done" to the devices a person has
// registered. Web push (VAPID) only for now; APNs rows are stored for the
// iOS app but not sent to.
package push

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Device struct {
	ID       uuid.UUID
	UserID   uuid.UUID
	Platform string
	Endpoint string
	P256dh   string
	Auth     string
}

type DeviceStore interface {
	Upsert(ctx context.Context, userID uuid.UUID, platform, endpoint, p256dh, auth, userAgent string) error
	Remove(ctx context.Context, userID uuid.UUID, endpoint string) error
	RemoveEndpoint(ctx context.Context, endpoint string) error
	ForUser(ctx context.Context, userID uuid.UUID, platform string) ([]Device, error)
}

type PostgresDeviceStore struct{ pool *pgxpool.Pool }

func NewPostgresDeviceStore(pool *pgxpool.Pool) *PostgresDeviceStore {
	return &PostgresDeviceStore{pool: pool}
}

func (s *PostgresDeviceStore) Upsert(ctx context.Context, userID uuid.UUID, platform, endpoint, p256dh, auth, userAgent string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO push_devices (user_id, platform, endpoint, p256dh, auth, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (endpoint) DO UPDATE SET
			user_id = EXCLUDED.user_id, platform = EXCLUDED.platform,
			p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth,
			user_agent = EXCLUDED.user_agent, last_seen_at = NOW()`,
		userID, platform, endpoint, p256dh, auth, userAgent)
	if err != nil {
		return fmt.Errorf("upsert push device: %w", err)
	}
	return nil
}

func (s *PostgresDeviceStore) Remove(ctx context.Context, userID uuid.UUID, endpoint string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM push_devices WHERE user_id = $1 AND endpoint = $2`, userID, endpoint); err != nil {
		return fmt.Errorf("remove push device: %w", err)
	}
	return nil
}

func (s *PostgresDeviceStore) RemoveEndpoint(ctx context.Context, endpoint string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM push_devices WHERE endpoint = $1`, endpoint); err != nil {
		return fmt.Errorf("remove push endpoint: %w", err)
	}
	return nil
}

func (s *PostgresDeviceStore) ForUser(ctx context.Context, userID uuid.UUID, platform string) ([]Device, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, platform, endpoint, p256dh, auth
		FROM push_devices WHERE user_id = $1 AND platform = $2`, userID, platform)
	if err != nil {
		return nil, fmt.Errorf("list push devices: %w", err)
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.UserID, &d.Platform, &d.Endpoint, &d.P256dh, &d.Auth); err != nil {
			return nil, fmt.Errorf("scan push device: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
```

- [ ] **Step 3: Config.** In `pkg/config/config.go` add:

```go
// PushConfig holds the VAPID key pair web push is signed with. All three
// empty means push is off: runs, the cap and in-app toasts still work.
type PushConfig struct {
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	VAPIDSubject    string // mailto: address push services can reach
}

func (c PushConfig) Enabled() bool {
	return c.VAPIDPublicKey != "" && c.VAPIDPrivateKey != "" && c.VAPIDSubject != ""
}
```

Add `Push PushConfig` to `Config`. Where `Stripe:` is loaded, add:

```go
		Push: PushConfig{
			VAPIDPublicKey:  strings.TrimSpace(getEnv("VAPID_PUBLIC_KEY", "")),
			VAPIDPrivateKey: strings.TrimSpace(getEnv("VAPID_PRIVATE_KEY", "")),
			VAPIDSubject:    strings.TrimSpace(getEnv("VAPID_SUBJECT", "")),
		},
```

- [ ] **Step 4: RPCs.** Add fields `devices push.DeviceStore` and `pushCfg config.PushConfig` to `UserHandler`, plus `WithPush`. Then:

```go
var platformNames = map[userpb.PushPlatform]string{
	userpb.PushPlatform_PUSH_PLATFORM_WEB_PUSH: "web_push",
	userpb.PushPlatform_PUSH_PLATFORM_APNS:     "apns",
}

// RegisterPushDevice records where the caller's finished searches should be
// announced. Re-registering refreshes it.
func (h *UserHandler) RegisterPushDevice(ctx context.Context, req *connect.Request[userpb.RegisterPushDeviceRequest]) (*connect.Response[commonpb.Response], error) {
	userID, err := callerUserID(ctx)
	if err != nil {
		return nil, err
	}
	if h.devices == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("push is not configured"))
	}
	platform, ok := platformNames[req.Msg.GetPlatform()]
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown platform"))
	}
	if platform == "web_push" && (req.Msg.GetP256Dh() == "" || req.Msg.GetAuth() == "") {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("web push needs p256dh and auth"))
	}
	if err := h.devices.Upsert(ctx, userID, platform, req.Msg.GetEndpoint(), req.Msg.GetP256Dh(), req.Msg.GetAuth(), req.Header().Get("User-Agent")); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&commonpb.Response{Success: true}), nil
}

func (h *UserHandler) UnregisterPushDevice(ctx context.Context, req *connect.Request[userpb.UnregisterPushDeviceRequest]) (*connect.Response[commonpb.Response], error) {
	userID, err := callerUserID(ctx)
	if err != nil {
		return nil, err
	}
	if h.devices != nil {
		if err := h.devices.Remove(ctx, userID, req.Msg.GetEndpoint()); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	return connect.NewResponse(&commonpb.Response{Success: true}), nil
}

// GetPushConfig hands out the VAPID public key at runtime rather than as a
// build variable: the Workers Builds deploy has no env and races the
// Actions one, so a baked-in key would vanish whenever it won.
func (h *UserHandler) GetPushConfig(ctx context.Context, _ *connect.Request[userpb.GetPushConfigRequest]) (*connect.Response[userpb.PushConfig], error) {
	if _, err := callerUserID(ctx); err != nil {
		return nil, err
	}
	key := ""
	if h.pushCfg.Enabled() {
		key = h.pushCfg.VAPIDPublicKey
	}
	return connect.NewResponse(&userpb.PushConfig{VapidPublicKey: key}), nil
}
```

The generated getter name for `p256dh` may be `GetP256Dh`. Check `user.pb.go` and use what it generated.

- [ ] **Step 5: Handler unit tests** in `user_handler_test.go`, following its existing fake-service style:
  - Register with `UNSPECIFIED` → `InvalidArgument`.
  - Web push without keys → `InvalidArgument`.
  - A valid call → the fake store saw `Upsert` with `"web_push"`.
  - `GetPushConfig` with a disabled config → empty key.

Run: `go test ./internal/domain/user/... ./internal/domain/push/ && go build ./...`
Expected: PASS.

- [ ] **Step 6: Wire.** In `dependencies.go`, `d.PushDevices = push.NewPostgresDeviceStore(d.DB.Pool)` (add the field). Then `d.UserHandler = userhandler.NewUserHandler(d.UserSvc).WithPush(d.PushDevices, d.Config.Push)`.

- [ ] **Step 7: Commit**

```bash
git add internal/domain/push/devices.go internal/domain/push/devices_integration_test.go pkg/config/config.go internal/domain/user/handler/ cmd/api/dependencies.go
git commit -m "Let a browser register for push, and hand it the VAPID key at runtime"
```

### Task 10: The notifier — a finished run becomes a push

**Files:**
- Create: `internal/domain/push/message.go`, `internal/domain/push/message_test.go`
- Create: `internal/domain/push/sender.go`, `internal/domain/push/sender_test.go`
- Create: `internal/domain/push/notifier.go`, `internal/domain/push/notifier_test.go`
- Modify: `cmd/api/dependencies.go` (replace `WithRuns(d.RunStore, nil)` with the notifier)
- Modify: `go.mod` (`go get github.com/SherClockHolmes/webpush-go@latest`)

**Interfaces:**
- Consumes: `runs.Run`, `runs.Store.ClaimNotification`, `runs.ResultPath`, `runs.FinishListener` (Phase 1); `DeviceStore` (Task 9); the user repo's `GetNotificationSettings`.
- Produces:

```go
type Payload struct { SessionID string `json:"sessionId"`; CityName string `json:"cityName"`; Domain string `json:"domain"`; Status string `json:"status"`; Title string `json:"title"`; Body string `json:"body"`; URL string `json:"url"` }
func BuildPayload(run runs.Run) Payload
type Sender interface { Send(ctx context.Context, d Device, body []byte) (gone bool, err error) }
func NewWebPushSender(cfg config.PushConfig, client *http.Client) *WebPushSender
type SettingsReader interface { GetNotificationSettings(ctx context.Context, userID uuid.UUID) (*locitypes.NotificationSettings, error) }
func NewNotifier(claims runs.Store, settings SettingsReader, devices DeviceStore, sender Sender, logger *slog.Logger) *Notifier
func (n *Notifier) OnRunFinished(ctx context.Context, run runs.Run) // a runs.FinishListener; returns immediately
```

- [ ] **Step 1: Failing payload test**

```go
package push

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/runs"
)

func TestBuildPayload(t *testing.T) {
	sid := uuid.MustParse("3043fb3f-15e8-461f-86de-6426fb389df2")
	p := BuildPayload(runs.Run{SessionID: sid, Domain: "itinerary", CityName: "Crete", Status: runs.StatusDone})
	require.Equal(t, "Your Crete itinerary is ready", p.Title)
	require.Equal(t, "Tap to open it.", p.Body)
	require.Equal(t, "done", p.Status)
	require.Equal(t, "/itinerary?sessionId="+sid.String()+"&cityName=Crete&domain=itinerary", p.URL)

	p = BuildPayload(runs.Run{SessionID: sid, Domain: "accommodation", Status: runs.StatusFailed})
	require.Equal(t, "Your hotels didn't finish", p.Title)
	require.Equal(t, "Tap to try again.", p.Body)
	require.Equal(t, "failed", p.Status)

	for domain, noun := range map[string]string{"general": "itinerary", "activities": "activities", "dining": "restaurants", "nearby": "nearby places"} {
		require.Equal(t, "Your Lisbon "+noun+" is ready", BuildPayload(runs.Run{SessionID: sid, Domain: domain, CityName: "Lisbon", Status: runs.StatusDone}).Title)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/domain/push/ -run BuildPayload`
Expected: FAIL, undefined.

- [ ] **Step 3: Implement `message.go`**

```go
package push

import (
	"strings"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/runs"
)

// Payload is what the service worker (and, later, the iOS app) receives.
// sessionId / cityName / domain are the keys the iOS client routes on.
type Payload struct {
	SessionID string `json:"sessionId"`
	CityName  string `json:"cityName"`
	Domain    string `json:"domain"`
	Status    string `json:"status"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	URL       string `json:"url"`
}

var nouns = map[string]string{
	"accommodation": "hotels",
	"dining":        "restaurants",
	"activities":    "activities",
	"nearby":        "nearby places",
}

func BuildPayload(run runs.Run) Payload {
	noun, ok := nouns[run.Domain]
	if !ok {
		noun = "itinerary"
	}
	subject := strings.TrimSpace(run.CityName + " " + noun)
	path, routeType, _ := runs.ResultPath(run.Domain, run.SessionID, run.CityName, uuid.Nil)
	p := Payload{
		SessionID: run.SessionID.String(),
		CityName:  run.CityName,
		Domain:    routeType,
		URL:       path,
	}
	if run.Status == runs.StatusDone {
		p.Status, p.Title, p.Body = "done", "Your "+subject+" is ready", "Tap to open it."
	} else {
		p.Status, p.Title, p.Body = "failed", "Your "+subject+" didn't finish", "Tap to try again."
	}
	return p
}
```

- [ ] **Step 4: Failing sender test.** Use an `httptest` server as the push service. Generate a VAPID pair with `webpush.GenerateVAPIDKeys()`. For the subscription keys, generate a P-256 key with `ecdh.P256().GenerateKey(rand.Reader)` (`p256dh` = base64url of `PublicKey().Bytes()`) and a 16-byte `auth` (base64url, no padding).
  - A 201 → `gone == false, err == nil`.
  - A 410 → `gone == true, err == nil`.
  - A 500 → `err != nil`.
  - Assert the request carries `TTL: 3600` and `Urgency: high`.

Run: `go test ./internal/domain/push/ -run Sender`
Expected: FAIL, undefined.

- [ ] **Step 5: Implement `sender.go`**

```go
package push

import (
	"context"
	"fmt"
	"io"
	"net/http"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/FACorreiaa/loci-connect-api/pkg/config"
)

type Sender interface {
	Send(ctx context.Context, d Device, body []byte) (gone bool, err error)
}

type WebPushSender struct {
	cfg    config.PushConfig
	client *http.Client
}

func NewWebPushSender(cfg config.PushConfig, client *http.Client) *WebPushSender {
	if client == nil {
		client = http.DefaultClient
	}
	return &WebPushSender{cfg: cfg, client: client}
}

func (s *WebPushSender) Send(ctx context.Context, d Device, body []byte) (bool, error) {
	resp, err := webpush.SendNotificationWithContext(ctx, body, &webpush.Subscription{
		Endpoint: d.Endpoint,
		Keys:     webpush.Keys{P256dh: d.P256dh, Auth: d.Auth},
	}, &webpush.Options{
		HTTPClient:      s.client,
		Subscriber:      s.cfg.VAPIDSubject,
		VAPIDPublicKey:  s.cfg.VAPIDPublicKey,
		VAPIDPrivateKey: s.cfg.VAPIDPrivateKey,
		TTL:             3600,
		Urgency:         webpush.UrgencyHigh,
	})
	if err != nil {
		return false, fmt.Errorf("web push: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return true, nil
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return false, nil
	default:
		return false, fmt.Errorf("web push: status %d", resp.StatusCode)
	}
}
```

- [ ] **Step 6: Failing notifier tests** (fakes for all four dependencies; `OnRunFinished` then wait for the background send with a `sync.WaitGroup` exposed as a test hook `n.wait()`):
  - Claim returns false → no settings read, no send.
  - `SearchFinished == false` → no send.
  - Two devices, one gone (`true`) → `RemoveEndpoint` called for that one only.
  - A sender error → logged, no panic, and the other device is still sent to.
  - The sent body unmarshals to a `Payload` with the expected title.

Run: `go test ./internal/domain/push/ -run Notifier -race`
Expected: FAIL, undefined.

- [ ] **Step 7: Implement `notifier.go`**

```go
package push

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/runs"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

type SettingsReader interface {
	GetNotificationSettings(ctx context.Context, userID uuid.UUID) (*locitypes.NotificationSettings, error)
}

// Notifier announces a finished run once, to every web-push device its
// owner registered, unless they switched "search finished" off. It never
// blocks the stream that finished and never fails it.
type Notifier struct {
	claims   runs.Store
	settings SettingsReader
	devices  DeviceStore
	sender   Sender
	logger   *slog.Logger
	wg       sync.WaitGroup
}

func NewNotifier(claims runs.Store, settings SettingsReader, devices DeviceStore, sender Sender, logger *slog.Logger) *Notifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &Notifier{claims: claims, settings: settings, devices: devices, sender: sender, logger: logger}
}

func (n *Notifier) OnRunFinished(_ context.Context, run runs.Run) {
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		n.deliver(ctx, run)
	}()
}

// wait is for tests.
func (n *Notifier) wait() { n.wg.Wait() }

func (n *Notifier) deliver(ctx context.Context, run runs.Run) {
	claimed, err := n.claims.ClaimNotification(ctx, run.ID)
	if err != nil || !claimed {
		if err != nil {
			n.logger.Warn("claim run notification", "run_id", run.ID, "error", err)
		}
		return
	}
	settings, err := n.settings.GetNotificationSettings(ctx, run.UserID)
	if err != nil {
		n.logger.Warn("read notification settings", "user_id", run.UserID, "error", err)
		return
	}
	if !settings.SearchFinished {
		return
	}
	devices, err := n.devices.ForUser(ctx, run.UserID, "web_push")
	if err != nil || len(devices) == 0 {
		return
	}
	body, err := json.Marshal(BuildPayload(run))
	if err != nil {
		return
	}
	for _, d := range devices {
		gone, err := n.sender.Send(ctx, d, body)
		switch {
		case gone:
			if rErr := n.devices.RemoveEndpoint(ctx, d.Endpoint); rErr != nil {
				n.logger.Warn("remove gone push endpoint", "error", rErr)
			}
		case err != nil:
			n.logger.Warn("push send failed", "run_id", run.ID, "device_id", d.ID, "error", err)
		}
	}
}
```

If the codebase has a metrics helper (grep `observability.` counters in `pkg/observability`), add a `push_sent_total{result}` counter next to the logs. If there is no counter pattern, logs are enough.

- [ ] **Step 8: Wire.** In `dependencies.go`, after `d.RunStore` and `d.PushDevices` exist:

```go
	var onRunFinish runs.FinishListener
	if d.Config.Push.Enabled() {
		notifier := push.NewNotifier(d.RunStore, d.UserRepo, d.PushDevices,
			push.NewWebPushSender(d.Config.Push, &http.Client{Timeout: 10 * time.Second}), d.Logger)
		onRunFinish = notifier.OnRunFinished
	} else {
		d.Logger.Info("web push disabled: VAPID_PUBLIC_KEY / VAPID_PRIVATE_KEY / VAPID_SUBJECT not all set")
	}
	d.ChatHandler = chathandler.NewChatHandler(d.ChatService, d.Logger, d.RecommendationHandler).WithRuns(d.RunStore, onRunFinish)
```

`d.UserRepo` is whatever field holds the `PostgresUserRepo`; it satisfies `SettingsReader` already. If only the service is held, `d.UserSvc` works too, since it has the same method. The handler init must run after this wiring; move the `onRunFinish` block above `initHandlers` or into it.

- [ ] **Step 9: Run everything**

Run: `go test ./internal/domain/push/ ./internal/domain/runs/ ./cmd/api/ -race && go build ./... && golangci-lint run ./...`
Expected: PASS, 0 issues.

- [ ] **Step 10: Commit and open PR B**

```bash
git add internal/domain/push/ cmd/api/dependencies.go go.mod go.sum
git commit -m "Send a web push when a search finishes or fails"
git push -u origin HEAD
gh pr create --title "Tell people by push when a search finishes" --body "Implements Phase 2 of docs/superpowers/plans/2026-09-22-background-run-notifications.md. Needs VAPID keys sealed into loci-env (horus) — without them push is skipped and nothing else changes."
```

- [ ] **Step 11: Hand the user the sealing step (they run it).**
  - Generate the keys: `go run github.com/SherClockHolmes/webpush-go/cmd/...` if the module ships a CLI; otherwise a 10-line `main` calling `webpush.GenerateVAPIDKeys()`.
  - Point them at `~/Work/production/platform/infra/secrets/*/README.md` for the per-repo sealing procedure. The blob must be sealed for namespace **`horus`**, secret **`loci-env`**, or the keys silently never appear.
  - After merge, open the infra promote PR by hand. CD's promote step has reported success while doing nothing. Check that the image tag contains the merge commit.

---

## Phase 3 — Client PR A: registry, toasts, server fallback

Work in a worktree: `git -C ~/Work/production/apps/Loci/loci-client worktree add -b feat/run-notifications ../.wt-client-runs origin/main`.

### Task 11: Contracts, server loaders and keyed completed sessions

**Files:**
- Modify: `package.json` (`@buf/loci_loci-proto.bufbuild_es` → the BSR commit from Task 1)
- Modify: `src/lib/api/llm.ts` (add `getSessionList`, `getRunStatuses`)
- Create: `src/lib/streaming/completed-sessions.ts` + `src/lib/streaming/completed-sessions.test.ts`
- Modify: `src/lib/streaming/restore-session.ts` (`readCompletedSession` reads the map first)
- Modify: `src/lib/streaming-service.ts` `finalize()` (write the map)

**Interfaces:**
- Produces:

```ts
// llm.ts
export type SessionListSection = "general" | "hotels" | "restaurants" | "activities";
export const getSessionList: (sessionId: string, section: SessionListSection) => Promise<{ city?: GeneralCityData; pois: POIDetailedInfo[] }>;
export type RunState = "running" | "done" | "failed";
export interface RunInfo { sessionId: string; status: RunState; url: string; cityName: string; domain: string }
export const getRunStatuses: (sessionIds: string[]) => Promise<RunInfo[]>;
// completed-sessions.ts
export function saveCompletedSession(sessionId: string, data: unknown): void; // keeps the newest 5
export function loadCompletedSession(sessionId: string): Record<string, unknown> | null;
```

- [ ] **Step 1: Bump the contract.** Set the exact version string from the Task 1 `buf push` in `package.json`, then run `pnpm install`. Then:

`grep -c getRunStatus node_modules/@buf/loci_loci-proto.bufbuild_es/loci/chat/chat_pb.d.ts`
Expected: ≥ 1. If 0, the BSR commit is wrong: stop and check it.

- [ ] **Step 2: Failing test for the completed-session map**

```ts
import { beforeEach, describe, expect, it } from "vitest";
import { loadCompletedSession, saveCompletedSession } from "./completed-sessions";

describe("completed sessions", () => {
  beforeEach(() => sessionStorage.clear());

  it("keeps several finished sessions side by side", () => {
    saveCompletedSession("a", { session_id: "a", hotels: [1] });
    saveCompletedSession("b", { session_id: "b", activities: [2] });
    expect(loadCompletedSession("a")).toEqual({ session_id: "a", hotels: [1] });
    expect(loadCompletedSession("b")).toEqual({ session_id: "b", activities: [2] });
  });

  it("drops the oldest beyond five", () => {
    for (const id of ["1", "2", "3", "4", "5", "6"]) saveCompletedSession(id, { session_id: id });
    expect(loadCompletedSession("1")).toBeNull();
    expect(loadCompletedSession("6")).toEqual({ session_id: "6" });
  });

  it("survives garbage in storage", () => {
    sessionStorage.setItem("loci.completedSessions", "{not json");
    expect(loadCompletedSession("a")).toBeNull();
  });
});
```

Run: `pnpm vitest run src/lib/streaming/completed-sessions.test.ts`
Expected: FAIL, module not found.

- [ ] **Step 3: Implement `completed-sessions.ts`**

```ts
// Finished sessions, keyed by id, so several searches can finish in one tab
// without each overwriting the last. The single "completedStreamingSession"
// slot is still written for older readers; this map is what restores read
// first.

const KEY = "loci.completedSessions";
const KEEP = 5;

type Entry = { id: string; data: unknown };

function read(): Entry[] {
  try {
    const raw = sessionStorage.getItem(KEY);
    const parsed = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

export function saveCompletedSession(sessionId: string, data: unknown): void {
  if (!sessionId) return;
  const next = [...read().filter((e) => e.id !== sessionId), { id: sessionId, data }].slice(-KEEP);
  try {
    sessionStorage.setItem(KEY, JSON.stringify(next));
  } catch {
    /* private mode / quota: the server fallback still works */
  }
}

export function loadCompletedSession(sessionId: string): Record<string, unknown> | null {
  const hit = read().find((e) => e.id === sessionId);
  return hit && hit.data && typeof hit.data === "object" ? (hit.data as Record<string, unknown>) : null;
}
```

In `restore-session.ts`, at the top of `readCompletedSession` after the `!sessionId` guard, add `const keyed = loadCompletedSession(sessionId); if (keyed) return (keyed.data ?? keyed) as Record<string, unknown>;`. The map stores what `finalize` stored: the session object, whose `.data` is the payload. In `streaming-service.ts` `finalize()`, next to the existing `sessionStorage.setItem(COMPLETED_SESSION_KEY, …)`, add `saveCompletedSession(s.sessionId, s);`.

- [ ] **Step 4: Server loaders in `llm.ts`** (imports: `SessionPOISection`, `RunStatus` from `chat_pb.js`):

```ts
export type SessionListSection = "general" | "hotels" | "restaurants" | "activities";

const sectionEnum: Record<SessionListSection, SessionPOISection> = {
  general: SessionPOISection.GENERAL,
  hotels: SessionPOISection.HOTELS,
  restaurants: SessionPOISection.RESTAURANTS,
  activities: SessionPOISection.ACTIVITIES,
};

/** A finished session's list, loaded from the server (first 50). */
export const getSessionList = async (
  sessionId: string,
  section: SessionListSection,
): Promise<{ city?: GeneralCityData; pois: POIDetailedInfo[] }> => {
  const [session, list] = await Promise.all([
    chatClient.getChatSession({ sessionId }),
    chatClient.getSessionPOIs({
      sessionId,
      section: sectionEnum[section],
      pagination: { page: 1, pageSize: 50 },
    }),
  ]);
  return {
    city: mapGeneralCityData(session.session?.currentItinerary?.generalCityData),
    pois: list.pointsOfInterest.map(mapPoi),
  };
};

export type RunState = "running" | "done" | "failed";
export interface RunInfo {
  sessionId: string;
  status: RunState;
  url: string;
  cityName: string;
  domain: string;
}

const runState: Partial<Record<RunStatus, RunState>> = {
  [RunStatus.RUNNING]: "running",
  [RunStatus.DONE]: "done",
  [RunStatus.FAILED]: "failed",
};

/** Where the caller's runs are. Unknown ids are simply absent. */
export const getRunStatuses = async (sessionIds: string[]): Promise<RunInfo[]> => {
  if (sessionIds.length === 0) return [];
  const res = await chatClient.getRunStatus({ sessionIds: sessionIds.slice(0, 20) });
  return res.runs.flatMap((r) => {
    const status = runState[r.status];
    return status
      ? [{ sessionId: r.sessionId, status, url: r.url, cityName: r.cityName, domain: String(r.domain) }]
      : [];
  });
};
```

Check the generated enum member names in `chat_pb.d.ts` (`SessionPOISection.HOTELS` vs `SESSION_POI_SECTION_HOTELS`: `bufbuild/es` v2 strips the prefix) and match them. The field names `currentItinerary` and `generalCityData` are the ones `getChatSession` already uses at llm.ts:614.

- [ ] **Step 5: Run tests and types**

Run: `pnpm vitest run src/lib/streaming && pnpm typecheck`
Expected: PASS, no type errors.

- [ ] **Step 6: Commit**

```bash
pnpm exec oxfmt src/lib/streaming/completed-sessions.ts src/lib/streaming/completed-sessions.test.ts src/lib/streaming/restore-session.ts src/lib/streaming-service.ts src/lib/api/llm.ts
git add package.json pnpm-lock.yaml src/lib/streaming/completed-sessions.ts src/lib/streaming/completed-sessions.test.ts src/lib/streaming/restore-session.ts src/lib/streaming-service.ts src/lib/api/llm.ts
git commit -m "Keep several finished searches, and load one from the server"
```

### Task 12: Multi-run registry

**Files:**
- Modify: `src/lib/streaming/live-stream-store.ts`
- Modify: `src/lib/streaming-service.ts`
- Modify: `src/lib/streaming/resume-live.ts`
- Modify: `src/lib/hooks/useChat.ts:323` (`streamingService.stop()` → `stop(sessionId)`)
- Test: `src/lib/streaming/live-stream-store.test.ts` (new or extend), `src/lib/streaming-service.test.ts` (extend)

**Interfaces:**
- Produces (live-stream-store):

```ts
export interface LiveStream { /* existing fields */ url: string; source: "service" | "page" }
export const liveRuns: Record<string, LiveStream>;          // solid store, keyed by sessionId
export function upsertRun(sessionId: string, patch: Partial<LiveStream>): void;
export function removeRun(sessionId: string): void;
export function isLiveSession(sessionId?: string | null): boolean;       // unchanged signature
export function useLiveSession(sessionId: () => string | undefined);      // unchanged signature
export function persistActiveSession(e: ActiveSessionEnvelope): void;     // now upserts into a list
export function readActiveSession(sessionId: string): ActiveSessionEnvelope | null; // unchanged
export function readActiveSessions(): ActiveSessionEnvelope[];
export function clearActiveSession(sessionId?: string): void;             // no id = clear all
export const MAX_RUNS = 3;
```

- Produces (streaming-service): `startStream(params, manager)` no longer stops other runs; `stop(sessionId?: string)` (no id = stop all); `cleanup()` stops all.

- [ ] **Step 1: Failing registry tests**

```ts
import { beforeEach, describe, expect, it } from "vitest";
import {
  clearActiveSession,
  isLiveSession,
  liveRuns,
  persistActiveSession,
  readActiveSessions,
  removeRun,
  upsertRun,
} from "./live-stream-store";

const env = (id: string) => ({
  sessionId: id, requestId: "r", lastEventId: "", query: "q", domain: "itinerary" as const, city: "Crete", startedAt: 1,
});

describe("run registry", () => {
  beforeEach(() => {
    for (const id of Object.keys(liveRuns)) removeRun(id);
    clearActiveSession();
  });

  it("tracks several runs at once", () => {
    upsertRun("a", { phase: "streaming", domain: "itinerary", city: "Crete" });
    upsertRun("b", { phase: "streaming", domain: "dining", city: "Porto" });
    expect(isLiveSession("a")).toBe(true);
    expect(isLiveSession("b")).toBe(true);
    upsertRun("a", { phase: "complete" });
    expect(liveRuns.b.phase).toBe("streaming");
  });

  it("persists one envelope per run and survives a reload", () => {
    persistActiveSession(env("a"));
    persistActiveSession(env("b"));
    persistActiveSession({ ...env("a"), lastEventId: "e9" });
    const all = readActiveSessions();
    expect(all.map((e) => e.sessionId).sort()).toEqual(["a", "b"]);
    expect(all.find((e) => e.sessionId === "a")?.lastEventId).toBe("e9");
    clearActiveSession("a");
    expect(readActiveSessions().map((e) => e.sessionId)).toEqual(["b"]);
  });

  it("reads an old single-envelope value", () => {
    sessionStorage.setItem("active_streaming_session", JSON.stringify(env("old")));
    expect(readActiveSessions().map((e) => e.sessionId)).toEqual(["old"]);
  });
});
```

Run: `pnpm vitest run src/lib/streaming/live-stream-store.test.ts`
Expected: FAIL (`liveRuns`/`upsertRun` not exported).

- [ ] **Step 2: Rewrite `live-stream-store.ts`.** Keep the header comment and the `isServer` note. Replace the single store:

```ts
export interface LiveStream {
  sessionId: string;
  requestId: string;
  domain: DomainType;
  city: string;
  query: string;
  phase: LiveStreamPhase;
  data: Partial<UnifiedChatResponse> | null;
  error: string | null;
  lastEventId: string;
  tokenCount: number;
  startedAt: number;
  /** The page that shows this run's result. */
  url: string;
  /** Who reads the stream: the shared service, or a page's own useChatRPC. */
  source: "service" | "page";
}

export const MAX_RUNS = 3;

const blank = (sessionId: string): LiveStream => ({
  sessionId, requestId: "", domain: "general", city: "", query: "", phase: "idle",
  data: null, error: null, lastEventId: "", tokenCount: 0, startedAt: Date.now(), url: "", source: "service",
});

const [liveRuns, setLiveRuns] = createStore<Record<string, LiveStream>>({});
export { liveRuns };

export function upsertRun(sessionId: string, patch: Partial<LiveStream>): void {
  if (isServer || !sessionId) return;
  setLiveRuns(
    produce((runs) => {
      runs[sessionId] = Object.assign(runs[sessionId] ?? blank(sessionId), patch);
    }),
  );
}

export function removeRun(sessionId: string): void {
  if (isServer) return;
  setLiveRuns(produce((runs) => void delete runs[sessionId]));
}

export function isLiveSession(sessionId: string | undefined | null): boolean {
  if (!sessionId) return false;
  const run = liveRuns[sessionId];
  return Boolean(run && run.phase !== "idle");
}

export function useLiveSession(sessionId: () => string | undefined) {
  const run = () => {
    const id = sessionId();
    return id && isLiveSession(id) ? liveRuns[id] : null;
  };
  return {
    isLive: () => run() !== null,
    data: () => run()?.data ?? null,
    phase: (): LiveStreamPhase => run()?.phase ?? "idle",
    error: () => run()?.error ?? null,
    isStreaming: () => {
      const p = run()?.phase;
      return p === "connecting" || p === "streaming";
    },
    tokenCount: () => run()?.tokenCount ?? 0,
  };
}
```

Envelopes become a list under the same key:

```ts
function readEnvelopes(): ActiveSessionEnvelope[] {
  if (isServer) return [];
  try {
    const raw = sessionStorage.getItem(ACTIVE_SESSION_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    // Before multi-run, one envelope was stored bare.
    return Array.isArray(parsed) ? parsed : parsed?.sessionId ? [parsed] : [];
  } catch {
    return [];
  }
}

function writeEnvelopes(list: ActiveSessionEnvelope[]): void {
  try {
    sessionStorage.setItem(ACTIVE_SESSION_KEY, JSON.stringify(list.slice(-MAX_RUNS)));
  } catch {
    /* private mode / quota: resume is best-effort */
  }
}

export function persistActiveSession(envelope: ActiveSessionEnvelope): void {
  if (isServer) return;
  writeEnvelopes([...readEnvelopes().filter((e) => e.sessionId !== envelope.sessionId), envelope]);
}

export function readActiveSession(sessionId: string): ActiveSessionEnvelope | null {
  if (!sessionId) return null;
  return readEnvelopes().find((e) => e.sessionId === sessionId) ?? null;
}

export function readActiveSessions(): ActiveSessionEnvelope[] {
  return readEnvelopes();
}

export function clearActiveSession(sessionId?: string): void {
  if (isServer) return;
  if (!sessionId) {
    try { sessionStorage.removeItem(ACTIVE_SESSION_KEY); } catch { /* ignore */ }
    return;
  }
  writeEnvelopes(readEnvelopes().filter((e) => e.sessionId !== sessionId));
}
```

Delete `liveStream`, `patchLiveStream` and `resetLiveStream`. Then `grep -rn "liveStream\b\|patchLiveStream\|resetLiveStream" src` must return only lines you are about to change.

- [ ] **Step 3: Make `streaming-service.ts` hold one `Run` per session.**
  - Replace `private run: Run | null` with `private runs = new Map<string, Run>()`, keyed by `requestId` (the session id is not known until `start`).
  - Add `sessionId: string` to `Run` (empty until `start`).
  - `startStream`: remove `this.stop();`. Build the run as today, `this.runs.set(run.requestId, run)`, and skip the initial `patchLiveStream` when `params.sessionId` is empty: a fresh search has no key yet.
    - **For a resume** (`params.sessionId` set): `run.sessionId = params.sessionId` and `upsertRun(params.sessionId, { requestId, domain, city, query, phase: "connecting", data: …, lastEventId: params.resumeToken ?? "", source: "service", url: getDomainRoute(manager.session.domain, params.sessionId, manager.session.city) })`.
  - Everywhere the file calls `patchLiveStream(x)` guarded by `if (this.run === run)`, call instead a private `live(run, patch)` that does `if (run.sessionId) upsertRun(run.sessionId, patch)`.
  - In `project`'s `case "start"`, set `run.sessionId = event.sessionId` **before** `this.publish(run, …)`, and include in the publish `url: getDomainRoute(mgr.session.domain, run.sessionId, mgr.session.city)`, `source: "service"`, `requestId: run.requestId`, `query: run.query`, `startedAt: Date.now()`.
  - `publish` reads `liveRuns[run.sessionId]?.lastEventId` / `startedAt` instead of `liveStream.*`.
  - The token counter becomes `live(run, { tokenCount: (liveRuns[run.sessionId]?.tokenCount ?? 0) + 1 })`.
  - `consume`'s `finally`: `this.runs.delete(run.requestId)`.
  - Stop and cleanup:

```ts
  /** Stop one run (by session id), or every run. */
  public stop(sessionId?: string): void {
    for (const run of this.runs.values()) {
      if (sessionId && run.sessionId !== sessionId) continue;
      run.aborted = true;
      run.controller.abort();
    }
  }

  public cleanup(): void {
    this.stop();
    this.runs.clear();
  }
```

  - `useChat.ts:323`: change `streamingService.stop()` to `streamingService.stop(<the session id that hook is showing>)`. Read the surrounding function to find it; if that hook genuinely means "stop everything", keep the no-arg call.

- [ ] **Step 4: `resume-live.ts`.** `resumeLiveSession(sessionId)` keeps its contract. Its `readActiveSession(sessionId)` now reads from the list, so the body needs no change beyond compiling. Add `resumeAllLive()`, used on app load by Task 14:

```ts
/** Re-attach every run a reload interrupted. Returns the ids resumed. */
export function resumeAllLive(): string[] {
  return readActiveSessions()
    .filter((e) => !readCompletedSession(e.sessionId))
    .flatMap((e) => (resumeLiveSession(e.sessionId) ? [e.sessionId] : []));
}
```

- [ ] **Step 5: Service test.** In `streaming-service.test.ts`, mock `streamChatEvents` (`vi.mock("./streaming/chatStream")`) to yield `start(sid)`, then wait on a deferred promise, then `complete`. Start two streams and assert:
  - both `liveRuns[a]` and `liveRuns[b]` are `streaming`;
  - `stop("a")` aborts only a's signal;
  - after b's complete, `liveRuns[b].phase === "complete"` and `liveRuns[b].url` starts with `/itinerary?sessionId=b`.

Run: `pnpm vitest run src/lib && pnpm typecheck`
Expected: PASS. Fix every type error in the four result routes; they should need none, since `useLiveSession` kept its shape.

- [ ] **Step 6: Commit**

```bash
pnpm exec oxfmt src/lib/streaming/live-stream-store.ts src/lib/streaming/live-stream-store.test.ts src/lib/streaming-service.ts src/lib/streaming-service.test.ts src/lib/streaming/resume-live.ts src/lib/hooks/useChat.ts
git add src/lib/streaming/live-stream-store.ts src/lib/streaming/live-stream-store.test.ts src/lib/streaming-service.ts src/lib/streaming-service.test.ts src/lib/streaming/resume-live.ts src/lib/hooks/useChat.ts
git commit -m "Let up to three searches stream at once instead of each new one stopping the last"
```

### Task 13: Page-owned streams report to the registry; result pages load from the server

**Files:**
- Modify: `src/lib/hooks/useChatRPC.ts`
- Create: `src/lib/streaming/hydrate-session.ts` + `src/lib/streaming/hydrate-session.test.ts`
- Modify: `src/routes/activities/index.tsx`, `src/routes/hotels/index.tsx`, `src/routes/restaurants/index.tsx` (the `onMount` restore chain), `src/routes/nearme/index.tsx`

**Interfaces:**
- Consumes: `upsertRun` (Task 12), `getSessionList`, `saveCompletedSession` (Task 11).
- Produces: `hydrateSession(sessionId: string, section: SessionListSection, listKey: "activities" | "hotels" | "restaurants" | "points_of_interest"): Promise<Record<string, unknown> | null>`.

- [ ] **Step 1: `useChatRPC` reports its run.** Keep the stream loop as it is. Add a `let sessionId = ""` before the loop, and at these points call the registry (import `upsertRun` and `getDomainRoute`):
  - `case "start"`: `sessionId = event.sessionId ?? ""; upsertRun(sessionId, { phase: "streaming", domain: (event.domain as DomainType) ?? "general", city: event.city ?? cityName ?? "", query: message, source: "page", startedAt: Date.now(), url: location.pathname + location.search });`. The url is replaced at completion, when the server's navigation tells us the real result page.
  - Every data case (`city_data`, list cases, `itinerary`): `upsertRun(sessionId, { data })` with the same object passed to `setState`.
  - `case "complete"`: `upsertRun(sessionId, { phase: "complete", url: event.navigation?.url || getDomainRoute(…) })`, then `saveCompletedSession(sessionId, { sessionId, data: state.streamedData })`.
  - `catch`: `if (sessionId) upsertRun(sessionId, { phase: "error", error: parsedError.userMessage });`.

Check the `LociStreamEvent` `start` variant's field names in `chatStream.ts` before using `event.sessionId` / `event.domain` / `event.city`. The service already reads exactly those three at streaming-service.ts `case "start"`.

- [ ] **Step 2: Failing hydrate test**

```ts
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("~/lib/api/llm", () => ({
  getSessionList: vi.fn(async () => ({ city: { city: "Crete" }, pois: [{ name: "Knossos" }] })),
}));

import { hydrateSession } from "./hydrate-session";
import { loadCompletedSession } from "./completed-sessions";

describe("hydrateSession", () => {
  beforeEach(() => sessionStorage.clear());

  it("builds the page payload from the server and caches it", async () => {
    const data = await hydrateSession("s1", "activities", "activities");
    expect(data).toEqual({ session_id: "s1", general_city_data: { city: "Crete" }, activities: [{ name: "Knossos" }] });
    expect(loadCompletedSession("s1")).toBeTruthy();
  });

  it("returns null for an empty session so the page can re-run", async () => {
    const { getSessionList } = await import("~/lib/api/llm");
    vi.mocked(getSessionList).mockResolvedValueOnce({ city: undefined, pois: [] });
    expect(await hydrateSession("s2", "activities", "activities")).toBeNull();
  });
});
```

Run: `pnpm vitest run src/lib/streaming/hydrate-session.test.ts`
Expected: FAIL, module not found.

- [ ] **Step 3: Implement `hydrate-session.ts`**

```ts
// A result page opened with a session id that nothing in this tab knows —
// the notification opened a fresh tab, or the tab was reloaded after the
// run finished. The result is stored server-side; load it instead of
// running (and paying for) the search again.

import { getSessionList, type SessionListSection } from "~/lib/api/llm";
import { saveCompletedSession } from "./completed-sessions";

export async function hydrateSession(
  sessionId: string,
  section: SessionListSection,
  listKey: "activities" | "hotels" | "restaurants" | "points_of_interest",
): Promise<Record<string, unknown> | null> {
  try {
    const { city, pois } = await getSessionList(sessionId, section);
    if (pois.length === 0 && !city) return null;
    const data = { session_id: sessionId, general_city_data: city, [listKey]: pois };
    saveCompletedSession(sessionId, { sessionId, data });
    return data;
  } catch {
    return null;
  }
}
```

- [ ] **Step 4: Use it in the routes.** In `activities/index.tsx`'s `onMount`, the chain is: resume live → `readCompletedSession` → re-run. Insert the server step before re-running:

```ts
      const restored = normalizeStoredData(readCompletedSession(sessionIdFromUrl));
      if (restored) {
        setRestoredData(restored);
        return;
      }
      void hydrateSession(sessionIdFromUrl, "activities", "activities").then((fromServer) => {
        if (fromServer) setRestoredData(normalizeStoredData(fromServer));
        else if (!state.isConnected) startOrExplain();
      });
      return;
```

Apply the same edit to `hotels` (`"hotels", "hotels"`) and `restaurants` (`"restaurants", "restaurants"`); each has the same `onMount` shape. Read each file first; the local helper names can differ slightly.

For `nearme/index.tsx`:
- Add a mount branch. If `searchParams.sessionId` is present, try `resumeLiveSession`. Then try `readCompletedSession`, then `hydrateSession(id, "general", "points_of_interest")`, and render that data instead of asking for geolocation.
- Read how it renders `state.streamedData` today and feed the restored data through the same path: a `restoredData` signal preferred over `state.streamedData`, as the other routes do.

- [ ] **Step 5: Run tests and types**

Run: `pnpm vitest run && pnpm typecheck && pnpm lint`
Expected: PASS. oxlint must report 0 warnings. CI runs `--deny-warnings`, which a plain local run does not.

- [ ] **Step 6: Commit**

```bash
pnpm exec oxfmt src/lib/hooks/useChatRPC.ts src/lib/streaming/hydrate-session.ts src/lib/streaming/hydrate-session.test.ts src/routes/activities/index.tsx src/routes/hotels/index.tsx src/routes/restaurants/index.tsx src/routes/nearme/index.tsx
git add src/lib/hooks/useChatRPC.ts src/lib/streaming/hydrate-session.ts src/lib/streaming/hydrate-session.test.ts src/routes/activities/index.tsx src/routes/hotels/index.tsx src/routes/restaurants/index.tsx src/routes/nearme/index.tsx
git commit -m "Keep tracking a search after leaving its page, and open a finished one without re-running it"
```

### Task 14: Toaster and RunWatcher

**Files:**
- Create: `src/lib/toast-store.ts`, `src/components/toast/Toaster.tsx`
- Create: `src/lib/runs/watcher-rules.ts` + `src/lib/runs/watcher-rules.test.ts`
- Create: `src/components/runs/RunWatcher.tsx`
- Modify: `src/app.tsx` (mount both inside `<AuthProvider>`, after `<UpgradePrompt />`, outside the `bare()` guard so a chromeless page still hears)

**Interfaces:**
- Produces:

```ts
// toast-store.ts
export interface Toast { id: string; title: string; action?: { label: string; href?: string; run?: () => void }; secondary?: { label: string; run: () => void } }
export const toasts: Toast[]; // solid store
export function showToast(t: Toast): void;   // same id replaces; max 3, oldest dropped
export function dismissToast(id: string): void;
// watcher-rules.ts
export function isOnRunPage(pathname: string, search: string, runUrl: string, sessionId: string): boolean;
export function runToast(run: { sessionId: string; phase: "complete" | "error"; domain: string; city: string; url: string }): Toast;
export function nounFor(domain: string): string;
```

- [ ] **Step 1: Failing rules tests**

```ts
import { describe, expect, it } from "vitest";
import { isOnRunPage, runToast } from "./watcher-rules";

describe("watcher rules", () => {
  const url = "/itinerary?sessionId=s1&cityName=Crete&domain=itinerary";

  it("knows when you are already looking at the result", () => {
    expect(isOnRunPage("/itinerary", "?sessionId=s1&cityName=Crete&domain=itinerary", url, "s1")).toBe(true);
    expect(isOnRunPage("/nearme", "", url, "s1")).toBe(false);
    expect(isOnRunPage("/itinerary", "?sessionId=other", url, "s1")).toBe(false);
  });

  it("words a finished and a failed run", () => {
    expect(runToast({ sessionId: "s1", phase: "complete", domain: "itinerary", city: "Crete", url }).title).toBe(
      "Your Crete itinerary is ready",
    );
    const failed = runToast({ sessionId: "s1", phase: "error", domain: "accommodation", city: "", url });
    expect(failed.title).toBe("Your hotels didn't finish");
    expect(failed.action?.label).toBe("Retry");
  });
});
```

Run: `pnpm vitest run src/lib/runs`
Expected: FAIL, module not found.

- [ ] **Step 2: Implement `watcher-rules.ts`**

```ts
import type { Toast } from "~/lib/toast-store";

// Same nouns as the server's push text (internal/domain/push/message.go).
const NOUNS: Record<string, string> = {
  accommodation: "hotels",
  hotels: "hotels",
  dining: "restaurants",
  restaurants: "restaurants",
  activities: "activities",
  nearby: "nearby places",
  nearme: "nearby places",
};

export const nounFor = (domain: string): string => NOUNS[domain] ?? "itinerary";

export function isOnRunPage(pathname: string, search: string, runUrl: string, sessionId: string): boolean {
  const target = new URL(runUrl, "https://x");
  return pathname === target.pathname && new URLSearchParams(search).get("sessionId") === sessionId;
}

export function runToast(run: {
  sessionId: string;
  phase: "complete" | "error";
  domain: string;
  city: string;
  url: string;
}): Toast {
  const subject = `${run.city} ${nounFor(run.domain)}`.trim();
  if (run.phase === "complete") {
    return { id: run.sessionId, title: `Your ${subject} is ready`, action: { label: "Open", href: run.url } };
  }
  return { id: run.sessionId, title: `Your ${subject} didn't finish`, action: { label: "Retry", href: run.url } };
}
```

Retry navigates to the run's page. Every result page already re-runs the query when its session restores nothing, and the failed run left nothing to restore, so no second retry path is needed.

- [ ] **Step 3: Implement `toast-store.ts` and `Toaster.tsx`**

```ts
// toast-store.ts — the app's one global toast list. Built for finished
// searches; keep it that small.
import { createStore, produce } from "solid-js/store";

export interface Toast {
  id: string;
  title: string;
  action?: { label: string; href?: string; run?: () => void };
  secondary?: { label: string; run: () => void };
}

const MAX = 3;
const [toasts, setToasts] = createStore<Toast[]>([]);
export { toasts };

export function showToast(t: Toast): void {
  setToasts(
    produce((list) => {
      const i = list.findIndex((x) => x.id === t.id);
      if (i >= 0) list.splice(i, 1);
      list.push(t);
      while (list.length > MAX) list.shift();
    }),
  );
}

export function dismissToast(id: string): void {
  setToasts((list) => list.filter((t) => t.id !== id));
}
```

```tsx
// Toaster.tsx
import { For } from "solid-js";
import { A } from "@solidjs/router";
import { dismissToast, toasts } from "~/lib/toast-store";

export default function Toaster() {
  return (
    <div
      role="status"
      aria-live="polite"
      class="fixed z-50 bottom-24 md:bottom-6 right-4 left-4 md:left-auto flex flex-col gap-2 md:w-96"
    >
      <For each={toasts}>
        {(t) => (
          <div class="rounded-xl border border-border bg-card text-card-foreground shadow-lg p-3 flex items-center gap-3">
            <p class="flex-1 text-sm font-medium">{t.title}</p>
            {t.secondary && (
              <button type="button" class="text-sm text-muted-foreground" onClick={() => t.secondary!.run()}>
                {t.secondary.label}
              </button>
            )}
            {t.action?.href ? (
              <A href={t.action.href} class="text-sm font-semibold text-primary" onClick={() => dismissToast(t.id)}>
                {t.action.label}
              </A>
            ) : t.action ? (
              <button type="button" class="text-sm font-semibold text-primary" onClick={() => { t.action!.run?.(); dismissToast(t.id); }}>
                {t.action.label}
              </button>
            ) : null}
            <button type="button" aria-label="Dismiss" class="text-muted-foreground" onClick={() => dismissToast(t.id)}>
              ×
            </button>
          </div>
        )}
      </For>
    </div>
  );
}
```

Use the design tokens the app already uses. Check `bg-card`/`border-border`/`text-primary` against an existing card component (e.g. `grep -rn "bg-card" src/components | head -3`) and match its classes. `bottom-24` clears the mobile bottom bar (`pb-20` in app.tsx).

- [ ] **Step 4: Implement `RunWatcher.tsx`**

```tsx
// Watches every run in the registry. When one ends while you are on some
// other page, it says so, with a way back. It also settles runs a reload
// interrupted, by resuming them or asking the server how they ended.
import { createEffect, on, onMount } from "solid-js";
import { useLocation } from "@solidjs/router";
import { liveRuns, readActiveSessions, upsertRun, clearActiveSession } from "~/lib/streaming/live-stream-store";
import { resumeAllLive } from "~/lib/streaming/resume-live";
import { getRunStatuses } from "~/lib/api/llm";
import { showToast } from "~/lib/toast-store";
import { isOnRunPage, runToast } from "~/lib/runs/watcher-rules";

export default function RunWatcher() {
  const location = useLocation();
  const announced = new Set<string>();

  const announce = (sessionId: string) => {
    const run = liveRuns[sessionId];
    if (!run || announced.has(sessionId)) return;
    if (run.phase !== "complete" && run.phase !== "error") return;
    announced.add(sessionId);
    clearActiveSession(sessionId);
    if (isOnRunPage(location.pathname, location.search, run.url, sessionId)) return;
    showToast(runToast({ sessionId, phase: run.phase, domain: run.domain, city: run.city, url: run.url }));
  };

  createEffect(
    on(
      () => Object.values(liveRuns).map((r) => `${r.sessionId}:${r.phase}`).join(","),
      () => Object.keys(liveRuns).forEach(announce),
    ),
  );

  onMount(async () => {
    const pending = readActiveSessions().map((e) => e.sessionId);
    const resumed = new Set(resumeAllLive());
    const orphaned = pending.filter((id) => !resumed.has(id));
    if (orphaned.length === 0) return;
    try {
      for (const info of await getRunStatuses(orphaned)) {
        if (info.status === "running") continue;
        upsertRun(info.sessionId, {
          phase: info.status === "done" ? "complete" : "error",
          url: info.url,
          city: info.cityName,
          domain: info.domain as never,
        });
      }
    } catch {
      /* signed out or offline: the pages still restore on their own */
    }
  });

  return null;
}
```

`info.domain` arrives as the proto enum's string. Map it to the client's `DomainType` with the same mapping the stream uses (check `chatStream.ts`, which converts the enum at `start`) instead of the `as never` placeholder. Import that mapper.

- [ ] **Step 5: Mount.** In `src/app.tsx`, import both components and render them inside `<AuthProvider>` after the chrome `Show` block:

```tsx
                          <Toaster />
                          <RunWatcher />
```

- [ ] **Step 6: Watcher test.** Add `src/components/runs/RunWatcher.test.tsx` using `@solidjs/testing-library` if it is in devDependencies (check `package.json`). Render inside a `MemoryRouter` at `/nearme`, `upsertRun("s1", { phase: "streaming", url: "/itinerary?sessionId=s1", city: "Crete", domain: "itinerary" })`, then set `phase: "complete"`, and assert `toasts[0].title === "Your Crete itinerary is ready"`. Repeat at `/itinerary?sessionId=s1` and assert no toast. If there is no Solid testing library, rely on the rules tests from Step 1 plus browser QA and say so in the PR body.

Run: `pnpm vitest run && pnpm typecheck && pnpm lint`
Expected: PASS, 0 warnings.

- [ ] **Step 7: Commit and open client PR A**

```bash
pnpm exec oxfmt src/lib/toast-store.ts src/components/toast/Toaster.tsx src/lib/runs/watcher-rules.ts src/lib/runs/watcher-rules.test.ts src/components/runs/RunWatcher.tsx src/app.tsx
git add src/lib/toast-store.ts src/components/toast/Toaster.tsx src/lib/runs/ src/components/runs/ src/app.tsx
git commit -m "Say when a search finishes while you are on another page, with a way back to it"
git push -u origin HEAD
gh pr create --title "Tell people when a search they left finishes" --body "Phase 3 of loci-connect-server docs/superpowers/plans/2026-09-22-background-run-notifications.md. Needs server PR A deployed (GetRunStatus, cap)."
```

After merge: check the live bundle for `api.lociai.fyi` in `connect-transport-*.js` (crawl two levels, see the double-deploy note) and re-dispatch "Deploy to Production" if Workers Builds won.

---

## Phase 4 — Client PR B: web push

### Task 15: Push subscription and the in-context permission prompt

**Files:**
- Create: `src/lib/push/prompt-rules.ts` + `src/lib/push/prompt-rules.test.ts`
- Create: `src/lib/push/push-client.ts`
- Modify: `src/components/runs/RunWatcher.tsx` (prompt on leaving a streaming run's page; re-register on sign-in)

**Interfaces:**
- Produces:

```ts
// prompt-rules.ts
export const DISMISS_KEY = "loci.pushPrompt.dismissedAt";
export function shouldOfferPush(o: { permission: "default" | "granted" | "denied" | "unsupported"; hasKey: boolean; dismissedAt: number | null; now: number }): boolean;
// push-client.ts
export async function getVapidKey(): Promise<string>;                       // "" when push is off; cached per page load
export async function enablePush(): Promise<"granted" | "denied" | "unsupported" | "off">;
export async function refreshPushRegistration(): Promise<void>;             // granted → re-subscribe + RegisterPushDevice
```

- [ ] **Step 1: Failing rules test**

```ts
import { describe, expect, it } from "vitest";
import { shouldOfferPush } from "./prompt-rules";

const DAY = 86_400_000;
describe("shouldOfferPush", () => {
  const base = { permission: "default" as const, hasKey: true, dismissedAt: null, now: 100 * DAY };
  it("offers when it could help and was never declined", () => expect(shouldOfferPush(base)).toBe(true));
  it("never re-asks a browser that blocked us", () => expect(shouldOfferPush({ ...base, permission: "denied" })).toBe(false));
  it("does not offer what the server cannot send", () => expect(shouldOfferPush({ ...base, hasKey: false })).toBe(false));
  it("waits 30 days after Not now", () => {
    expect(shouldOfferPush({ ...base, dismissedAt: base.now - 29 * DAY })).toBe(false);
    expect(shouldOfferPush({ ...base, dismissedAt: base.now - 31 * DAY })).toBe(true);
  });
  it("has nothing to offer once granted", () => expect(shouldOfferPush({ ...base, permission: "granted" })).toBe(false));
});
```

Run: `pnpm vitest run src/lib/push`
Expected: FAIL, module not found.

- [ ] **Step 2: Implement `prompt-rules.ts`**

```ts
export const DISMISS_KEY = "loci.pushPrompt.dismissedAt";
const THIRTY_DAYS = 30 * 86_400_000;

export function shouldOfferPush(o: {
  permission: "default" | "granted" | "denied" | "unsupported";
  hasKey: boolean;
  dismissedAt: number | null;
  now: number;
}): boolean {
  if (o.permission !== "default" || !o.hasKey) return false;
  return o.dismissedAt === null || o.now - o.dismissedAt > THIRTY_DAYS;
}

export function readDismissedAt(): number | null {
  try {
    const v = Number(localStorage.getItem(DISMISS_KEY));
    return Number.isFinite(v) && v > 0 ? v : null;
  } catch {
    return null;
  }
}

export function rememberDismissed(now = Date.now()): void {
  try {
    localStorage.setItem(DISMISS_KEY, String(now));
  } catch {
    /* private mode: they may be asked again next time */
  }
}
```

- [ ] **Step 3: Implement `push-client.ts`**

```ts
// Subscribes this browser to web push and tells the server where to send.
// The VAPID public key comes from GetPushConfig at runtime, never from a
// VITE_ variable (the Workers Builds deploy has no env).
import { createClient } from "@connectrpc/connect";
import {
  UserService,
  PushPlatform,
} from "@buf/loci_loci-proto.bufbuild_es/loci/user/user_pb.js";
import { transport } from "~/lib/connect-transport";
import { ensureNotificationPermission, getNotificationPermission } from "~/lib/notification-prefs";

const userClient = createClient(UserService, transport);
let keyPromise: Promise<string> | null = null;

export function getVapidKey(): Promise<string> {
  keyPromise ??= userClient
    .getPushConfig({})
    .then((r) => r.vapidPublicKey)
    .catch(() => "");
  return keyPromise;
}

const pushSupported = () =>
  typeof window !== "undefined" && "serviceWorker" in navigator && "PushManager" in window;

function toBytes(base64url: string): Uint8Array {
  const pad = "=".repeat((4 - (base64url.length % 4)) % 4);
  const raw = atob((base64url + pad).replace(/-/g, "+").replace(/_/g, "/"));
  return Uint8Array.from(raw, (c) => c.charCodeAt(0));
}

async function subscribeAndRegister(key: string): Promise<void> {
  const reg = await navigator.serviceWorker.ready;
  const sub =
    (await reg.pushManager.getSubscription()) ??
    (await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: toBytes(key) }));
  const json = sub.toJSON();
  await userClient.registerPushDevice({
    platform: PushPlatform.WEB_PUSH,
    endpoint: sub.endpoint,
    p256dh: json.keys?.p256dh ?? "",
    auth: json.keys?.auth ?? "",
  });
}

export async function enablePush(): Promise<"granted" | "denied" | "unsupported" | "off"> {
  if (!pushSupported()) return "unsupported";
  const key = await getVapidKey();
  if (!key) return "off";
  const permission = await ensureNotificationPermission();
  if (permission !== "granted") return permission === "unsupported" ? "unsupported" : "denied";
  await subscribeAndRegister(key);
  return "granted";
}

export async function refreshPushRegistration(): Promise<void> {
  if (!pushSupported() || getNotificationPermission() !== "granted") return;
  const key = await getVapidKey();
  if (key) await subscribeAndRegister(key).catch(() => undefined);
}
```

Check the generated enum spelling (`PushPlatform.WEB_PUSH`) and the request field `p256dh` (bufbuild/es may camel-case it to `p256dh`) in `user_pb.d.ts`.

- [ ] **Step 4: Prompt from the watcher.** In `RunWatcher.tsx`, add an effect on `location.pathname + location.search`. When the route changes, look at the previous location. If a run in `liveRuns` is `connecting`/`streaming` and `isOnRunPage(prev…)` was true for it, then:
  - check `shouldOfferPush({ permission: getNotificationPermission(), hasKey: Boolean(await getVapidKey()), dismissedAt: readDismissedAt(), now: Date.now() })`;
  - if it passes, and this is the first offer this page load (a module-level flag), call:

```ts
showToast({
  id: "push-offer",
  title: `Searching ${run.city || "for you"}… Want a ping when it's ready?`,
  action: { label: "Allow", run: () => void enablePush() },
  secondary: { label: "Not now", run: () => { rememberDismissed(); dismissToast("push-offer"); } },
});
```

`action.run` executes inside the click handler, so the browser treats `Notification.requestPermission()` as user-initiated.

Also, in an effect on the auth state (use whatever `AuthProvider` exposes; grep `useAuth` for the signed-in accessor), call `void refreshPushRegistration()` once the user is signed in.

- [ ] **Step 5: Relay pushes from the service worker.** In `RunWatcher.tsx` `onMount`, when `"serviceWorker" in navigator`:

```ts
navigator.serviceWorker.addEventListener("message", (e: MessageEvent) => {
  const p = e.data?.lociPush;
  if (!p?.sessionId || announced.has(p.sessionId)) return;
  upsertRun(p.sessionId, { phase: p.status === "done" ? "complete" : "error", url: p.url, city: p.cityName, domain: p.domain });
});
```

The existing `announce` path then shows the toast, deduped by `announced`.

- [ ] **Step 6: Run and commit**

Run: `pnpm vitest run && pnpm typecheck && pnpm lint`
Expected: PASS.

```bash
pnpm exec oxfmt src/lib/push/prompt-rules.ts src/lib/push/prompt-rules.test.ts src/lib/push/push-client.ts src/components/runs/RunWatcher.tsx
git add src/lib/push/ src/components/runs/RunWatcher.tsx
git commit -m "Offer a push the first time someone leaves a search that is still running"
```

### Task 16: Service worker push and click handlers

**Files:**
- Create: `public/push-sw.js`
- Create: `src/lib/push/push-sw.test.ts`
- Modify: `vite.config.ts` (`workbox.importScripts`)

**Interfaces:**
- Produces: the global `self.__lociPush = { decide(payload, clients) → { kind: "relay", client } | { kind: "show" } , targetUrl(data) → string }`, exported for tests via `module.exports` when defined.

- [ ] **Step 1: Failing test**

```ts
import { describe, expect, it } from "vitest";
// Plain script, not a module: it attaches to globalThis and, under vitest, to module.exports.
import pushSw from "../../../public/push-sw.js";

const payload = { sessionId: "s1", title: "Your Crete itinerary is ready", body: "Tap to open it.", url: "/itinerary?sessionId=s1" };

describe("push-sw decide", () => {
  it("relays to a visible Loci tab instead of a system notification", () => {
    const visible = { visibilityState: "visible", url: "https://lociai.fyi/nearme" };
    expect(pushSw.decide(payload, [visible])).toEqual({ kind: "relay", client: visible });
  });
  it("shows a notification when every tab is hidden", () => {
    expect(pushSw.decide(payload, [{ visibilityState: "hidden", url: "https://lociai.fyi/" }])).toEqual({ kind: "show" });
    expect(pushSw.decide(payload, [])).toEqual({ kind: "show" });
  });
  it("only follows same-site paths on click", () => {
    expect(pushSw.targetUrl({ url: "/itinerary?sessionId=s1" })).toBe("/itinerary?sessionId=s1");
    expect(pushSw.targetUrl({ url: "https://evil.example/x" })).toBe("/");
  });
});
```

Run: `pnpm vitest run src/lib/push/push-sw.test.ts`
Expected: FAIL, file not found.

- [ ] **Step 2: Implement `public/push-sw.js`**

```js
/* Loci push handlers, imported into the generated Workbox service worker
 * (vite.config.ts → workbox.importScripts). Plain script: no bundler runs
 * over this file. */
(function (root) {
  function decide(payload, clients) {
    var visible = (clients || []).find(function (c) {
      return c.visibilityState === "visible";
    });
    // A Loci tab is on screen: let the page's own toast say it rather than
    // a system notification over the page. (Chrome tolerates skipping
    // showNotification occasionally; see the spec's fallback if it warns.)
    return visible ? { kind: "relay", client: visible } : { kind: "show" };
  }

  // Only same-site paths: a payload can never send someone off-site.
  function targetUrl(data) {
    var url = data && typeof data.url === "string" ? data.url : "/";
    return url.charAt(0) === "/" && url.charAt(1) !== "/" ? url : "/";
  }

  var api = { decide: decide, targetUrl: targetUrl };
  root.__lociPush = api;
  if (typeof module !== "undefined" && module.exports) module.exports = api;

  if (typeof root.addEventListener !== "function" || typeof root.registration === "undefined") return;

  root.addEventListener("push", function (event) {
    var payload = {};
    try {
      payload = event.data ? event.data.json() : {};
    } catch (e) {
      return;
    }
    event.waitUntil(
      root.clients.matchAll({ type: "window", includeUncontrolled: true }).then(function (clients) {
        var d = decide(payload, clients);
        if (d.kind === "relay") {
          d.client.postMessage({ lociPush: payload });
          return;
        }
        return root.registration.showNotification(payload.title || "Loci", {
          body: payload.body || "",
          tag: payload.sessionId || undefined,
          data: { url: targetUrl(payload) },
          icon: "/icons/icon-192x192.png",
        });
      }),
    );
  });

  root.addEventListener("notificationclick", function (event) {
    event.notification.close();
    var url = targetUrl(event.notification.data);
    event.waitUntil(
      root.clients.matchAll({ type: "window", includeUncontrolled: true }).then(function (clients) {
        var tab = clients.find(function (c) {
          return new URL(c.url).origin === root.location.origin;
        });
        if (tab) return tab.focus().then(function (c) { return c.navigate(url); });
        return root.clients.openWindow(url);
      }),
    );
  });
})(typeof self !== "undefined" ? self : globalThis);
```

Check that `/icons/icon-192x192.png` exists (`ls public/icons`); use the manifest's 192 icon path.

For the test import, if vitest will not import a CommonJS `public/` file directly, change the test to `const pushSw = (await import("../../../public/push-sw.js")).default ?? globalThis.__lociPush;` after the side-effect import.

- [ ] **Step 3: Import it into the generated worker.** In `vite.config.ts`, inside `workbox: { … }`, add:

```ts
    // Push + notificationclick handlers (public/push-sw.js). generateSW stays
    // in charge of precaching; this only adds listeners.
    importScripts: ["/push-sw.js"],
```

- [ ] **Step 4: Build and check the worker**

Run: `pnpm vitest run src/lib/push && pnpm build && grep -c "push-sw.js" .output/public/sw.js`
Expected: tests PASS; grep ≥ 1. If the worker has another name, find it with `ls .output/public/*.js | grep -i sw`.

- [ ] **Step 5: Commit**

```bash
pnpm exec oxfmt src/lib/push/push-sw.test.ts vite.config.ts
git add public/push-sw.js src/lib/push/push-sw.test.ts vite.config.ts
git commit -m "Show a finished search as a system notification when no Loci tab is on screen"
```

### Task 17: The "Search finished" switch

**Files:**
- Modify: `src/lib/api/notifications.ts`
- Modify: `src/components/features/Settings/NotificationSettings.tsx`
- Modify: `src/components/modals/QuickSettingsModal.tsx` (only if it lists the switches)

**Interfaces:**
- Consumes: `NotificationSettings.searchFinished` (proto), `enablePush`, `getNotificationPermission` (Task 15).
- Produces: `NotificationSettings.searchFinished: boolean` in the client type.

- [ ] **Step 1: API.** Add `searchFinished: boolean` to the `NotificationSettings` type. Map `response.searchFinished` in both query and mutation, and pass `searchFinished: changes.searchFinished` in the update request. Update the file comment: `searchFinished` is delivered as a push; the other two still are not.

- [ ] **Step 2: UI.** In `NotificationSettings.tsx`, add a third switch, following the existing two exactly:
  - Label "Search finished", description "A notification when a search you left finishes or fails."
  - When it is turned **on** and `getNotificationPermission() === "default"`, call `enablePush()` in the same click handler.
  - When permission is `"denied"`, show the line: "Notifications are blocked for this site in your browser settings."
  - Remove or scope any copy that says no switch sends anything, so it names only the two that still don't.

- [ ] **Step 3: Run and commit**

Run: `pnpm vitest run && pnpm typecheck && pnpm lint`
Expected: PASS.

```bash
pnpm exec oxfmt src/lib/api/notifications.ts src/components/features/Settings/NotificationSettings.tsx
git add src/lib/api/notifications.ts src/components/features/Settings/NotificationSettings.tsx
git commit -m "Add the Search finished switch — the first one that sends something"
git push
gh pr create --title "Web push when a search finishes" --body "Phase 4 of the background-run-notifications plan. Needs server PR B deployed and VAPID keys sealed."
```

### Task 18: Browser QA (required before calling this done)

Against production after both deploys, in Chrome, signed in with a throwaway account:

- [ ] Start "3 days in Crete" from the dashboard. When it navigates to `/itinerary?sessionId=…`, go to `/nearme` and run a search. Expect "Your Crete itinerary is ready · Open"; Open lands on the itinerary with results.
- [ ] Repeat, and on leaving the itinerary accept the push offer (Allow → browser prompt → Allow). Switch to another tab before it finishes. Expect a system notification; clicking it focuses the Loci tab on the result.
- [ ] Repeat, and close the Loci tab after leaving the page. Expect a system notification; clicking it opens a new tab on the result, with no second generation (check: no new row in `generation_runs` for that user).
- [ ] Start 4 searches in quick succession. The 4th shows "You have 3 searches running — wait for one to finish".
- [ ] Turn "Search finished" off; finish a search in a hidden tab. Expect no system notification.
- [ ] Reload mid-search on another page. The run resumes or settles, and the toast still arrives.
- [ ] Record the result in the memory file `loci-background-run-notifications.md`, and send the final RPC and message names to the iOS session: `GetRunStatus`, `RunInfo`, `RunStatus`, `CompletePayload.load_from_session`, `RegisterPushDevice(platform=PUSH_PLATFORM_APNS, endpoint=<token>)`, and the payload keys.
