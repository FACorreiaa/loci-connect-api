# Background run notifications — design

Date: 2026-09-22
Scope: loci-connect-server, loci-connect-proto, loci-client (web). iOS is a
separate spec that reuses the server half; Android later.

## Problem

A search (itinerary, activities, hotels, restaurants, near me) can take
minutes. Today the person has to sit on the page and wait. If they leave to do
something else — start a near-me search, browse Discover — one of two things
happens:

- The home/dashboard search keeps streaming on the module-level
  `streamingService`, but the client runs **one stream at a time**:
  `startStream` calls `stop()` first, so starting any other search cancels the
  pending one on the client (the server keeps generating, the client stops
  listening).
- A search started on `/activities`, `/hotels`, `/restaurants` or `/nearme`
  goes through `useChatRPC`, which is owned by the page component. After the
  page unmounts its events go to signals nobody renders.

Either way nothing tells them the result is ready, and nothing takes them
back to it.

## Goal

Someone can start a search, go anywhere else in the app (or close the tab),
and be told when it finishes — or fails — with one click back to the result
at `/itinerary?sessionId=…&cityName=…&domain=…` (or the domain's route).

Decisions taken with the user:

| Question | Decision |
|---|---|
| Scope | Web + server push. iOS separate spec. |
| When to ask browser permission | The first time they leave a page whose search is still running. |
| Concurrent searches per person | Up to 3. The 4th is refused with a clear message. |
| What notifies | Done **and** failed, all domains including near me. |
| Architecture | Durable run status + VAPID web push + multi-run client registry (approach A). |

Rejected: a client-only approach with a push relay (breaks when the tab
closes, which is the case push exists for); a single per-user `WatchRuns`
stream replacing per-search streams (rewrites the streaming model the result
pages are built on).

## What exists today (verified 2026-09-22)

- `ChatService.StreamChat` (chat.proto:908) serves every domain. Generation
  runs on `context.WithoutCancel` with a 3-minute handler timeout
  (chat_handler.go:137) and 5-minute worker timeouts
  (chat_process_stream.go:363); DB writes are detached too (:513, :737). On a
  failed `Send` the handler keeps draining into the resume buffer
  (chat_handler.go:260, :273).
- `resumebuf` is in-memory, per pod, 500 events, 15-minute idle expiry
  (resumebuf/resumebuf.go:20). A resume **replays and returns**
  (chat_handler.go:179-194): it never follows a still-running generation. With
  no buffer entry it starts a new generation.
- Four events share one `EventID` (`recommendationRunID`,
  chat_process_stream.go ~646/658/669/683), so resuming from any of them is
  ambiguous and client dedup drops real events.
- Results persist per session: `persistGenerations`
  (generation_write.go:131, called at chat_process_stream.go:565) and
  `UpdateSession` with `CurrentItinerary` (:688-695). `GetChatSession`
  (chat_handler.go:745) loads it.
- `StreamChat` requires auth (auth + quota interceptors), so every run has an
  owning user.
- No push delivery anywhere. `notification_settings` (migration 0092) holds
  `recommendations` and `trip_reminders` booleans; its own comment says
  delivery is separate work.
- The web client registers a service worker via `vite-plugin-pwa`
  (entry-client.tsx:9) with no push code, and has no app-wide toast.

## Design

### 1. Run records (server)

**Table `generation_runs`** — next free migration number, chosen at merge
time (other branches add migrations concurrently; main has had a duplicate
number before).

| Column | Type | Notes |
|---|---|---|
| `session_id` | UUID PK | The chat session id issued at start. |
| `user_id` | UUID NOT NULL → users ON DELETE CASCADE | Owner. Indexed with `status, started_at`. |
| `domain` | TEXT NOT NULL | itinerary / activities / accommodation / dining / nearme / general. |
| `city_name` | TEXT NOT NULL DEFAULT '' | For the notification text and URL. |
| `status` | TEXT NOT NULL CHECK IN ('running','done','failed') | |
| `error_code` | TEXT | Connect code name on failure. |
| `started_at` | TIMESTAMPTZ NOT NULL DEFAULT now() | |
| `finished_at` | TIMESTAMPTZ | |
| `notified_at` | TIMESTAMPTZ | Set once, by the notification claim (§2). |

**Lifecycle.**
- `StreamChat` inserts a `running` row once the session id is known, before
  the start event is sent.
- The detached pipeline sets `done` after `persistGenerations` and
  `UpdateSession` succeed, or `failed` (with `error_code`) on error or
  timeout. These writes use `context.WithoutCancel` like the existing ones.
- A resume of an existing session does not insert a row.

**Stale runs.** A `running` row with `started_at` older than 10 minutes is
reported as `failed` (`error_code = deadline_exceeded`) wherever it is read,
and ignored by the cap. No sweeper job. The pipeline's own timeouts are
3–5 minutes, so 10 minutes only catches crashed pods.

**Cap: 3 concurrent runs per user.** Counting and inserting happen in one
transaction under `pg_advisory_xact_lock(hashtext(user_id::text))`: count the
user's non-stale `running` rows; at ≥ 3 return `ResourceExhausted` with
message "You have 3 searches running — wait for one to finish". The check
runs before quota is consumed, so a refused search costs nothing.

**`ChatService.GetRunStatus(session_ids)`** returns, for each id **owned by
the caller**, `{session_id, domain, city_name, status, error_code,
finished_at}`. Ids that are not the caller's are omitted, not errors. Used by
web after a reload and by iOS on return to the app.

**Resume fixes.**
- **Follow live.** `resumebuf` gains `Subscribe(sessionID, afterEventID)`,
  which replays buffered events and then delivers new ones until the final
  event. The resume path uses it instead of replay-and-return.
- **Buffer gone.** On a resume request (`resume_token` set) with no buffer
  entry, if the run row is `done` or `failed`, the handler sends one terminal
  event that tells the client to load via `GetChatSession`. It no longer
  starts a fresh generation for that resume. A follow-up message on an
  existing session (no `resume_token`) still generates as today.
- **Unique event ids.** Every event gets its own id; the four events that
  share `recommendationRunID` each get their own.

Known limit, documented, not fixed: the buffer is per pod. At more than one
replica a resume that lands on another pod gets the buffer-gone path — still
correct via `GetRunStatus`/`GetChatSession`, just not live. Production runs
one replica.

### 2. Push delivery (server)

**Table `push_devices`.**

| Column | Type | Notes |
|---|---|---|
| `id` | UUID PK | |
| `user_id` | UUID NOT NULL → users ON DELETE CASCADE | Indexed. |
| `platform` | TEXT NOT NULL CHECK IN ('web_push','apns') | `apns` reserved for iOS. |
| `endpoint` | TEXT NOT NULL UNIQUE | Web push endpoint URL, or APNs token later. |
| `p256dh` | TEXT | Web push only. |
| `auth` | TEXT | Web push only. |
| `user_agent` | TEXT | For the settings list, later. |
| `created_at` | TIMESTAMPTZ NOT NULL DEFAULT now() | |
| `last_seen_at` | TIMESTAMPTZ NOT NULL DEFAULT now() | Refreshed on re-register. |

Re-registering an existing endpoint upserts it, including moving it to the
current user if the browser changed accounts.

**RPCs on `UserService`** (alongside `Get/UpdateNotificationSettings`):
- `RegisterPushDevice(platform, endpoint, p256dh, auth)`
- `UnregisterPushDevice(endpoint)`
- `GetPushConfig()` → `{vapid_public_key}`. Served by the API rather than a
  `VITE_` build variable: the Cloudflare Workers Builds deploy has no env
  vars and races the Actions deploy, so a build-time key would be missing
  whenever it wins. Returns empty when push is not configured; the client
  then never offers push.

**Setting.** `notification_settings.search_finished BOOLEAN NOT NULL DEFAULT
TRUE`, exposed through the existing get/update RPCs and shown as a "Search
finished" switch. It defaults on because the in-context browser prompt is the
real consent. It is the first switch that actually sends something; the
settings copy says so for this one only.

**Sending.** When a run becomes `done` or `failed`:
1. Claim: `UPDATE generation_runs SET notified_at = now() WHERE session_id =
   $1 AND notified_at IS NULL RETURNING user_id, domain, city_name, status`.
   No row means someone already notified; stop.
2. Skip if the user's `search_finished` is false.
3. In a background goroutine with its own timeout, send to each of the user's
   `web_push` devices with `github.com/SherClockHolmes/webpush-go`, VAPID,
   TTL 3600, urgency high.
4. `404`/`410` from the push service deletes that device. Other errors are
   logged and counted as a metric. A push failure never changes the run.

**Payload** (encrypted per RFC 8291 by the library):

```json
{
  "sessionId": "3043fb3f-…",
  "cityName": "Crete",
  "domain": "itinerary",
  "status": "done",
  "title": "Your Crete itinerary is ready",
  "body": "Tap to open it.",
  "url": "/itinerary?sessionId=3043fb3f-…&cityName=Crete&domain=itinerary"
}
```

- On failure the title is "Your Crete itinerary didn't finish" and the body
  is "Tap to try again."
- The route follows the domain: `/itinerary`, `/activities`, `/hotels`,
  `/restaurants`, `/nearme`, using the same mapping as the client's
  `getDomainRoute`.
- With no city the text drops it ("Your itinerary is ready").
- The keys `sessionId`, `cityName` and `domain` match what the iOS app uses
  in local-notification `userInfo`, so APNs reuses them later.

**Secrets.**
- `VAPID_PUBLIC_KEY`, `VAPID_PRIVATE_KEY` and `VAPID_SUBJECT`
  (`mailto:` address) go in `loci-env`, sealed for namespace `horus`.
- If they are missing, the server logs once at startup and skips sending.
  Runs, the cap, `GetRunStatus` and in-app toasts all work without them, so
  the server can deploy before the secret is sealed.

### 3. Web client

**Run registry.**
- `live-stream-store` becomes a keyed map of up to 3 runs. Each entry keeps
  today's `LiveStream` fields.
- `streamingService` holds one `Run` per session id. `startStream` no longer
  stops other runs, and `stop(sessionId)` stops one.
- `useChatRPC` starts its stream through the registry instead of owning it,
  so a near-me search survives leaving `/nearme`.
- `useLiveSession(sessionId)` and `isLiveSession(sessionId)` keep their
  current signatures, so result pages change little.
- The `sessionStorage` resume envelope becomes a list keyed by session id.
  `resumeLiveSession` re-attaches each run.
- On load, the client calls `GetRunStatus` for every id in the list and
  settles runs that finished while nobody was listening.
- A `ResourceExhausted` start shows the server's message inline where the
  search was typed.

**`RunWatcher`** — one component mounted in the app shell.
- When a run moves to `complete` or `error` and the current route is not that
  run's page (same path and `sessionId`), it shows a toast.
- Done: "Your Crete itinerary is ready · Open". Failed: "Your Crete itinerary
  didn't finish · Retry". Retry re-runs the original query.
- Toasts persist until dismissed or clicked, at most 3 stacked, and dedupe by
  session id, including against a push relayed from the service worker.
- This needs the app's first global toast: a small `Toaster` component and
  store, built for this and nothing else.

**Permission prompt.**
- The first time the route changes away from a run that is still streaming,
  and push is configured (`GetPushConfig` returns a key) and permission is
  `default`, the watcher shows: "Searching Crete… Want a ping when it's
  ready? · Allow · Not now".
- Allow → `ensureNotificationPermission()`, then
  `registration.pushManager.subscribe({userVisibleOnly: true,
  applicationServerKey})`, then `RegisterPushDevice`.
- "Not now" is remembered in `localStorage` for 30 days. A browser-level
  denial is never asked again.
- With permission already `granted`, the client re-subscribes and calls
  `RegisterPushDevice` after sign-in, which refreshes `last_seen_at`.

**Service worker.** Move `vite-plugin-pwa` to `injectManifest` so the worker
can carry custom handlers, keeping the existing precache behaviour.
- `push`: if a Loci window client is visible, `postMessage` the payload to it
  (the watcher toasts it, deduped) and do not show a system notification.
  Otherwise call `showNotification(title, {body, tag: sessionId, data:
  {url}})`. The `tag` makes a repeat replace rather than stack.
- `notificationclick`: close the notification, focus an existing Loci window
  and navigate it to `data.url`, or `clients.openWindow(data.url)`.
- Chrome expects every push to show a notification. Suppressing it while a
  tab is visible is tolerated occasionally. If Chrome starts showing its
  generic "site updated in the background" notice, switch to always showing
  the system notification and have the watcher skip its toast for that
  session.

## Failure handling

| Case | Behaviour |
|---|---|
| Push send fails / VAPID unset | Logged + counted. Run and in-app toast unaffected. |
| Browser lacks push / permission denied | In-app toast while a tab is open. No further prompts. |
| Safari on iPhone | Web push needs Loci installed to the Home Screen (iOS 16.4+). A Safari tab gets toasts only. |
| Pod restart mid-run | Row stays `running`, reads as `failed` after 10 min; client settles via `GetRunStatus`. |
| Resume on a pod without the buffer | Buffer-gone terminal event → `GetChatSession`. |
| 4th concurrent search | `ResourceExhausted`, inline message, no quota used. |

## Testing

**Server**
- Run lifecycle, including a client that disconnects mid-stream: the row
  still reaches `done`.
- A failing pipeline reaches `failed`.
- Cap: two concurrent starts at 2 running against real Postgres, exactly one
  succeeds.
- A stale `running` row does not count toward the cap.
- The notification claim is exactly-once under two concurrent completions.
- `GetRunStatus` never returns another user's run.
- Resume that follows a live run to completion; the buffer-gone path;
  uniqueness of event ids across one full stream.
- Push sender against an `httptest` push service: 201 success, 410 deletes
  the device, VAPID unset skips sending, `search_finished=false` skips.

**Client (vitest)**
- Registry: 3 concurrent runs, `stop(id)` leaves the others running, and the
  envelope list round-trips through reload.
- Watcher: no toast on the run's own page, toast elsewhere, dedupe by session
  id, retry wiring.
- Permission prompt: shown once, "Not now" suppressed for 30 days.
- Service-worker `push`/`notificationclick` logic extracted into pure
  functions and tested.

**Browser QA (manual, required before calling it done)**
1. Start a Crete itinerary, go to near me and search there. Expect the
   itinerary toast; clicking it lands on the result.
2. Same with the Loci tab in the background: expect a system notification;
   clicking it focuses the tab on the result.
3. Same with the tab closed: expect a system notification; clicking it opens
   a new tab on the result.
4. Start 4 searches: the 4th shows the limit message.
5. Force a failure: expect the "didn't finish" toast, and Retry works.

## Rollout

Each step is safe to deploy before the next.

1. **Proto.**
   - Add `GetRunStatus` with its messages; `RegisterPushDevice`,
     `UnregisterPushDevice` and `GetPushConfig`; and `search_finished` on
     the notification settings messages.
   - Release both halves: git tag plus `buf push`.
   - Before pushing, check the BSR state: an inconsistent PlaceIntelligence
     schema has previously reddened main's proto job. Verify the tag
     contains these RPCs.
2. **Server PR A.** Migration for `generation_runs`, lifecycle writes, cap,
   `GetRunStatus`, resume fixes.
3. **Server PR B.**
   - Migration for `push_devices` and `search_finished`, the device RPCs,
     `GetPushConfig`, and the sender.
   - The user seals the VAPID keys into `loci-env` (horus).
   - Open the infra promote PR by hand: CD's promote step has been a no-op.
4. **Client PR A.** Run registry, `useChatRPC` through the registry,
   `RunWatcher` + `Toaster`, `GetRunStatus` on load. On its own this fixes
   the reported scenario while a tab is open.
5. **Client PR B.** Permission prompt, `injectManifest` service worker with
   push and click handlers, "Search finished" switch.

After each client merge, check the live bundle for `api.lociai.fyi` (the
Workers Builds race). Once the proto is tagged, send the final RPC and
message names to the iOS session.

## Out of scope

- APNs sending and the iOS client (separate spec; `platform = apns` and the
  payload keys are reserved for it).
- Android.
- Email notifications.
- A shared resume buffer across replicas.
- A settings screen listing registered devices (`user_agent` is stored for
  it).
