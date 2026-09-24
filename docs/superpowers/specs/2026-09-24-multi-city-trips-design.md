# Multi-city trips — design

Date: 2026-09-24 · Repos: loci-connect-proto, loci-connect-server, loci-client, loci-ios

## Problem

A traveller who wants "Lisbon for three days, then Porto for two" cannot get one
plan for it. Every generated result is one city: `ChatRequest` produces one
`AiCityResponse` with one `general_city_data`. The web keeps it in
`useStreamedRpc(message, cityName)` and iOS in `SearchState.cityName`. The only
multi-city path is Compare → `MultiCityPlan` → a saved Trip, which has legs but no
hotels, restaurants or activities, and never shows up on the result screens.

## Goals

1. One trip spanning 2–5 cities, on web and iOS.
2. For each city: a day-by-day itinerary sized to the nights it gets, plus hotels,
   restaurants and activities.
3. Travel legs between cities with a suggested mode, distance and rough duration,
   and the travel day marked in the plan.
4. A suggested visiting order when the traveller gives cities without one.
5. Two ways in: a chat message ("Lisbon 3 days then Porto 2") and a stop builder
   (add a city, set nights, reorder).
6. Save, reopen offline and share work the way they do for single-city results.

## Non-goals

- Booking or payments. This is recommendations only; existing outbound links stay.
- Live transport schedules or prices. Leg mode and duration are estimates and the
  UI labels them as estimates.
- Follow-up edits by chat on a multi-city session ("swap Porto for Braga"). In v1
  the traveller edits the stops in the builder and regenerates.
- Gating. Everyone gets up to five cities.
- New Telegram UX. Telegram goes through the same pipeline and must not break.

## What already exists and is reused

| Piece | Where | Used for |
|---|---|---|
| `multicity.Plan(Input) Route` — pure, no I/O: nearest-neighbour order, day allocation, legs, travel share, warnings, outline | `internal/domain/multicity/plan.go` | Route suggestion and legs |
| `TripDay.city_name/city_id/travel_day`, `TripDraft.legs[]` | `loci-connect-proto/proto/loci/trip/trip.proto:100,135` | Persisting the whole trip |
| `loadLegs` / `insertLegs` | `internal/domain/trip/repository.go:290,375` | Same |
| `compare/v1 MultiCityPlan {cities, legs, warnings, outline}` | `compare/v1/compare.proto:116` | Route payload on the stream |
| `CityResolver.Resolve` (geocoder that creates the city row) | compare service | Resolving each typed city |
| Chat run auto-saves a Trip | `chat_process_stream.go:708` → `buildTripFromCityResponse` (`chat_trip.go:91`) | Per-stop days → parent Trip |
| Web `MultiCityPlanCard.tsx`, `trips/[id].tsx` leg list, `TripGlobe.tsx` | loci-client | Leg rows, route drawing |
| iOS `TripEditorView` legs section, `ResultsMapCard` auto-fit camera | loci-ios | Leg rows, map |

## Approach

Run the existing single-city pipeline once per city, inside one stream.

Two alternatives were rejected:

- **One LLM call for every city.** Output grows with the number of cities, which is
  the failure behind the blank-itinerary bug (reasoning tokens ate `max_tokens`).
  It also can't use the per-city generation cache.
- **A separate, non-streaming multi-city RPC.** It would lose the chat entry point,
  the progress UI and the Telegram path.

Each city runs as a normal single-city **child chat session**. Reopening a result,
the hotels, restaurants and activities pages, and the generation cache then work
for each city without changes. One **parent Trip** ties the cities together.

## Contract (loci-connect-proto, additive, one minor tag)

```proto
// chat.proto
message TripStopInput {
  string city_name = 1;          // 1..200
  optional int32 nights = 2;     // 1..14; absent = planner decides
  optional string city_id = 3;
}
message ChatRequest {
  // ...existing fields...
  repeated TripStopInput stops = N;  // max 5; present => skip city extraction
  bool suggest_order = N+1;          // reorder the stops for the traveller
}

enum StreamEventType { /* ... */ STREAM_EVENT_TYPE_ROUTE = 13; }

message StopRef {
  int32 index = 1;
  string city_name = 2;
  string city_id = 3;
  string session_id = 4;         // the child session for this city
  repeated int32 day_numbers = 5;
}
message RoutePayload {
  loci.compare.v1.MultiCityPlan plan = 1;
  repeated StopRef stops = 2;
  string trip_id = 3;            // parent Trip, set once persisted
}
message StreamEvent {
  // ...existing fields...
  optional int32 stop_index = N;   // set on every per-city payload
  // oneof payload gains: RoutePayload route = 32;
}

// trip.proto
message TripCity {
  string city_name = 1;
  string city_id = 2;
  string session_id = 3;
  int32 nights = 4;
  int32 order_index = 5;
}
message TripDraft { /* ... */ repeated TripCity cities = N; }
// TripLeg.mode documented as "train" | "bus" | "flight" | "drive".
```

Rules:

- If an event stream has no `ROUTE` event, it is single-city and has exactly
  today's shape. Old clients ignore the new fields.
- `ROUTE` is sent before the first per-city event. When the parent Trip is saved at
  the end of the run, a second `ROUTE` carries `trip_id`.
- Every per-city payload in a multi-city stream sets `stop_index`.

## Server (loci-connect-server)

### Detect

`extractCityFromMessage` (`chat_converters.go:30`) returns
`cities[] {name, nights?}` and `ordered bool`:

- "Lisbon 3 days then Porto 2" → ordered, nights given.
- "Lisbon, Porto and Seville in a week" → unordered, total days 7.
- One city → today's path, untouched.

When `ChatRequest.stops` is set, the extractor is skipped. More than five cities
returns `InvalidArgument` from the request path, or a clear message in chat.

### Route

Each city is resolved with `CityResolver`. There are two cases:

- **Unordered, or `suggest_order` set:** `multicity.Plan` with `MaxCities: 5`.
  Cities it drops come back in `MultiCityPlan.dropped`, with the reason, so the UI
  can explain them.
- **Ordered, with nights:** build `DayPlan`s from the traveller's own nights and
  reuse `buildLegs`. The traveller's order and nights are kept, not overridden.
  Warnings, such as a heavy travel share, are still computed.

`multicity` gets a `modeFor(distanceKm)` heuristic that replaces the hard-coded
`"drive"`. It picks train or drive up to 300 km, train from 300 to 700 km and
flight above 700 km. Duration comes from the same estimate, and the UI shows it as
"≈".

### Generate

A new file, `internal/domain/chat/service/chat_multicity.go`, runs these steps:

1. Emit `ROUTE`.
2. For each stop in order, create a child session. Run the existing unified
   pipeline with `days = len(stop.day_numbers)`, and tag every event with
   `stop_index`.
3. Stops run one after another, not in parallel, because of LLM slots and the free
   tier.
4. If a stop fails, emit an `ERROR` tagged with its `stop_index` and go on to the
   next stop. The run completes as long as at least one stop succeeds.
5. Emit `COMPLETE` once, at the end.

### Persist

Child sessions do not auto-save their own Trip. At the end of the run, one parent
Trip is saved:

- Days are renumbered across all stops, and each day carries its city.
- `travel_day` is set on the day each leg departs.
- The parent Trip stores the `legs` and a `cities[]` entry linking each city to
  its child session.

Migration **0101_trip_cities** adds the `trip_cities` table (trip_id, order_index,
city_name, city_id, session_id, nights). Run `scripts/check-migrations.sh` before
committing, and renumber if another PR takes 0101 first. `SaveTrip`'s signature
does not change, because the guided-onboarding work plans to hook it.

### Follow-ups and Telegram

`ContinueSessionStreamed` and `startsNewTrip()` (from api #85) start a new trip
when a message names another city. A message that names two or more cities starts
a multi-city trip. A follow-up on a multi-city session is answered with a pointer
to the builder.

Telegram sends the route outline and then a short summary for each city. The
check is that it doesn't break; there is no new UX.

## Web (loci-client)

- **Builder** in the discover composer: city chips, each with a nights stepper,
  drag to reorder, a "Suggest order" toggle, and a cap of five cities. It sends
  `stops[]`.
- **State:** `useStreamedRpc` holds `route` and `stops: StreamingState[]`, keyed by
  `stop_index`. Without a `ROUTE` event it behaves exactly as it does today.
- **Results:** a stop switcher sits above the existing itinerary, hotels,
  restaurants and activities views. Its chips read like "Lisbon · 3n → Porto · 2n".
  Each view renders one stop through `lib/results/domain.ts`. The itinerary also
  has an "All" view: the days across every city, with a travel-day banner and a
  leg card reused from `MultiCityPlanCard`. The map fits every stop's POIs and
  draws the route line.
- **Save, share and offline:**
  - `OfflineItinerary.payload` gains `kind: "multi"` holding the route and each
    stop's response.
  - Saving to the cloud saves the parent Trip id.
  - `/saved` lists the trip as "Lisbon + Porto".
  - Share text is grouped by city, then by day.

## iOS (loci-ios)

- **Composer:** a stops builder sheet with the same controls as the web builder.
- **State:** `SearchState` holds `route` and `[StopState]`.
- **Results:** `ResultsPage` gets a stop picker. The itinerary list shows city
  headers and leg rows, reusing the `TripEditorView` leg row. `ResultsMapCard`
  already fits whatever pins it is given.
- **Save and offline:** the envelope in `SearchEnvelope.swift` gains a stops array,
  and Saved shows the parent Trip.
- **Collisions:** north-loci-onboarding-verdict has local branches that touch
  `SearchSessionController.finish()`, `SearchResultsView`, `DesignPreview` and
  `project.pbxproj`. Rebase onto them, or check with that session, before editing
  those files.

## Error handling

| Case | Behaviour |
|---|---|
| City can't be resolved | That stop is dropped with a reason in `dropped`; the others continue |
| Every city fails to resolve | `InvalidArgument`: "couldn't find any of these cities" |
| More than 5 cities | Rejected by validation (`max_items: 5`); chat asks the traveller to pick 5 |
| Route infeasible (travel share over the ceiling) | Warnings shown; the traveller can still generate |
| A stop's generation fails | Tagged `ERROR`; that stop shows a retry; the others render |
| Client disconnects mid-run | Existing background-run and resume path; `GetRunStatus` covers the parent run |

## Release (all four together)

1. Proto: merge, tag, then `buf push`. Check that the tag contains the change.
2. Server: `go get` the tag and `make generate`, then the PR. It is backward
   compatible.
3. Promote PR in infra. It is opened by hand because `INFRA_TOKEN` is unset.
   Deploy, then verify.
4. Web and iOS PRs merge the same day, after the server is verified. Grep the live
   web bundle after the merge, because of the double-deploy race.

## Testing

- **Go unit tests:**
  - The extractor on ordered, unordered and single-city messages.
  - Day plans built from the traveller's own nights.
  - `modeFor`.
  - The parent Trip merge: days renumbered, legs' `afterDay`, cities mapped to
    their sessions.
  - A failed stop doesn't stop the run.
  - A single-city stream is unchanged, checked with a golden test of its event
    types.
- **Local stream check** with Bruno or `buf curl`: "Lisbon 3 days then Porto 2
  days" returns `ROUTE`, then tagged events for both stops. `GetTrip` returns the
  legs and each day's city.
- **Web browser QA:** the chat path and the builder path, the stop switcher, the
  hotels page for each stop, save → `/saved` → reopen offline, and the share text.
- **iOS simulator:** the same flow, plus save and reopen.
- **Production:** one multi-city request, with pod logs showing generation for each
  stop and cache hits on a second run.
