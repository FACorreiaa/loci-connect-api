# Trip workflow with agent actions and flights — design

Date: 2026-09-30 · Repos: loci-connect-proto, loci-connect-server, loci-client, loci-ios

## Problem

A traveller cannot take one trip from an idea to a plan they can use. Four pieces
are missing:

1. **Dates.** `TripDraft` has no start or end date. `TripDay.date` exists, but
   only the calendar's "Pin dates" rail ever sets it. `pkg/tripspan` parses "12–17
   Nov" into a day count and then drops the dates.
2. **A chosen hotel.** Hotels are POIs with a `star_rating` string. `GetNearbyHotels`
   can filter by stars, but a trip has nowhere to record the one you picked.
3. **Flights.** There is no flight code. `multicity/plan.go` labels a leg over
   700 km `mode="flight"` and nothing more.
4. **An agent that can act.** Chat routes on keywords and regexes and has no tool
   calling. `ChatRequest.trip_id` is parsed and then ignored: every turn saves a
   new trip (`SaveTrip(tr, 0)`, `chat_process_stream.go:782-790`). "Book me 4-star
   hotels" regenerates a whole answer and changes nothing.

## Goals

1. One trip holds dates, a stay per city, flights, and days sized to the dates.
2. Every step works by hand in the trip editor, on web and iOS.
3. The same steps work from chat, on web, iOS and Telegram. The agent **proposes**
   a typed change, the traveller confirms, and the server applies it through the
   same code path as the manual editor.
4. Flight search costs nothing and needs no signup: the server builds deep links
   to Google Flights and Skyscanner, and the traveller saves the flight they chose.

## Non-goals

- In-app flight or hotel booking, and live prices. The server never shows a price
  it didn't get from the traveller.
- Native LLM tool calling. The keyless chain runs free NVIDIA models, whose
  `tools` support is unreliable.
- MCP tools for trips. Part B2 makes them cheap to add later.
- Paid flight APIs. The `Provider` interface leaves room for Travelpayouts or
  SerpApi later.

## Decisions (owner, 2026-09-30)

| Question | Choice |
|---|---|
| Agent writes | Propose → confirm, like Watches |
| Flights v1 | Deep links only |
| Phase 1 surfaces | Server, web, iOS, Telegram |

## Data model

**`TripDraft` additions** (proto and `trips` table):

- `optional string start_date`, `optional string end_date`, both `YYYY-MM-DD` and
  validated by protovalidate as `end_date >= start_date` and a span of 30 days or
  less, the same cap as `tripspan`.
- `repeated TripStay stays`. `TripStay` has `city_name`, `poi_id`, `name`,
  `star_rating`, `check_in`, `check_out` and `booking_url`. Stays are keyed by
  city name rather than hung on `TripCity`, because a single-city trip keeps its
  city on the draft and has no `cities` entries.
- `repeated TripFlight flights`. `TripFlight` has:
  - `id`
  - `origin`, `destination` (each a `FlightPlace{name, optional iata}`)
  - `depart_date`, `optional return_date`
  - `passengers`, `cabin` (enum)
  - `repeated FlightLink links` (`provider`, `url`)
  - optional user-entered `carrier`, `flight_no`, `price_text`, `notes`

  `price_text` is free text so we never imply a quoted fare.

**Dates drive days.** `SetTripDates` stamps `TripDay.date` on each existing day
(day 1 = start). It never adds or drops days on its own. When the day count and
the span disagree, the response says so and the client offers `RegenerateDays`.
The calendar overlay (`calendar/events.go:22`) and the ICS feed already read
`TripDay.date`, so a dated trip appears there with no calendar changes.

**Tables** (one migration, next free number after 0108):

- `trips.start_date date`, `trips.end_date date`
- `trip_stays(trip_id, city_name, poi_id, name, star_rating, check_in, check_out,
  booking_url)`, unique on `(trip_id, city_name)`
- `trip_flights(id, trip_id, …)`
- `trip_action_proposals(id uuid, user_id, trip_id, session_id, action jsonb,
  options jsonb, summary, status enum(pending, applied, dismissed, expired),
  created_at, expires_at)`

## Trip service

Mutations live inside the Connect handler today (`trip/handler.go:194` `mutate`,
through line 338). They move into `trip.Service`, whose methods take a user id
and an expected version and return the new `TripDraft`. The handler becomes thin.
Chat, Telegram and a future MCP all call the service, so there is one code path
for validation, ownership and optimistic locking.

New methods and RPCs, each with `expected_version`:

| RPC | Effect |
|---|---|
| `SetTripDates` | Sets start and end dates and stamps the day dates |
| `SetStay` / `ClearStay` | Upserts or deletes a city's stay |
| `AddFlight` / `RemoveFlight` | Adds or removes a flight on the trip |
| `RegenerateDays(days)` | Re-plans the itinerary to N days, using the existing chat generation with the trip's cities and constraints, and keeps stays and flights |
| `BuildFlightLinks(query)` | Stateless; returns links for the manual form |

## Flights

`pkg/flights`:

```go
type Query struct {
    Origin, Destination Place // Name always set; IATA optional
    Depart              time.Time
    Return              *time.Time
    Passengers          int
    Cabin               Cabin
}

type Provider interface {
    Links(Query) []Link
}
```

v1 is `DeepLinkProvider`:

- **Google Flights**, always:
  `https://www.google.com/travel/flights?q=Flights to {dest} from {origin} on {D1} through {D2}`,
  or `... on {D1} one way`. City names work, so no IATA code is needed.
- **Skyscanner**, only when both IATA codes are present:
  `https://www.skyscanner.net/transport/flights/{o}/{d}/{YYMMDD}[/{YYMMDD}]/?adultsv2={n}&cabinclass={c}&rtn={0|1}`.

The research that found these formats could not verify them (DeepAPI ran out of
credits), so each one gets a manual check before release. IATA codes come from
the action extractor and must match `^[A-Z]{3}$`, or they are dropped.

## Agent actions

### Action set

`chat.proto`, `TripAction` oneof:

| Action | Fields | Kind |
|---|---|---|
| `set_dates` | start, end | write |
| `set_constraint` | the `TripConstraint` fields | write |
| `search_hotels` | city, min_stars, max_stars | pick one of N |
| `regenerate_days` | days | write |
| `search_flights` | origin, destination, depart, return, passengers, cabin | pick one of N |
| `add_poi` / `remove_poi` | poi or name, day | write (replaces the keyword path) |

Picking an option from `search_hotels` applies `SetStay`. Picking one from
`search_flights` applies `AddFlight` with that link set.

### Flow

1. **Bind.** A chat turn is trip-bound when `ChatRequest.trip_id` is set, or
   when the session already has a trip. A bound turn never creates a new trip.
2. **Extract.** `actions.Extract(ctx, msg, snapshot)` makes one LLM call on the
   existing provider chain. The prompt carries a compact trip snapshot (cities,
   dates, day count, stays, flights) and a strict JSON schema with one example
   per action. The output is decoded into Go types and validated: dates parse,
   end ≥ start, stars 1–5, days 1–30, cities are on the trip or named in the
   message. On invalid JSON the call is retried once. On a second failure it
   returns no actions and logs `trip_action_extract_failed`, and the turn
   continues as normal chat.
3. **Resolve.**
   - `search_hotels` calls `POIService.GetNearbyHotels` with the star filter and
     keeps the top 5 as options.
   - `search_flights` calls `flights.Provider` and keeps the link sets as options.
   - A write action has no options.
4. **Store and emit.** Each action becomes a `trip_action_proposals` row with a
   24h expiry, and one `action_proposal` stream event is emitted with
   `{id, trip_id, action, summary, options}`. The prose answer still streams.
   For example, "Set dates to 12–17 Nov and look for 4-star hotels" produces a
   short reply plus two cards.
5. **Apply.** `ApplyTripAction(proposal_id, option_index?, expected_version)`:
   - loads the proposal
   - checks the owner, `pending` status and expiry
   - calls the trip service
   - marks the proposal `applied`
   - appends a confirmation message to the chat session
   - returns the `TripDraft` plus an `ActionAppliedPayload`

   `DismissTripAction` marks the proposal `dismissed`.

### Why proposals live on the server

- Telegram `callback_data` is capped at 64 bytes, and `a:<uuid>:<n>` fits.
- The client can't change the action between proposing and applying it.
- Each proposal is applied once: a second apply returns `FailedPrecondition`.

### Errors

| Case | Result |
|---|---|
| Stale `expected_version` | `Aborted`; the client refetches the trip and retries once |
| Expired, applied or dismissed proposal | `FailedPrecondition`; the card shows "no longer valid" |
| Not the owner | `NotFound` |
| Extractor fails | No cards; the chat answer still arrives |
| Hotel search returns nothing | The card says "no {n}-star hotels found" and offers to widen the range (a new proposal) |

## Surfaces

**Web (`loci-client`):**

- Trip editor "Plan" panel with four sections: Dates, Stay per city, Days,
  Flights. The date input reuses the calendar's "Pin dates" logic.
- `ActionProposalCard.tsx`, modelled on `WatchProposalCard.tsx`. A write action
  shows Confirm / Not now. A pick action shows a list with one tap per option.
- New cases in `streaming-service.ts`. `useChat` sends `tripId` when opened from
  a trip. Applying invalidates the trip query.

**iOS (`loci-ios`):**

- The same four sections in `TripEditorView`.
- Proposal cards in `MuseThread`, copying the Watch confirm.
- `SearchState` handles the new payloads.
- The proto bump and handling of the new oneof cases land in one PR, since the
  new cases break the build until handled.

**Telegram:**

- A proposal is sent as a message with inline buttons. A write action gets
  ✓ / ✕. A pick action gets one button per option plus ✕.
- A `callback_query` handler next to the existing "More" paging calls
  `ApplyTripAction` and edits the message to show the result.
- `ContinueChat` from the bridge passes the latest session's trip.

## Testing

**Server:**

- Extractor table tests, about 20 prompts against a fake `ChatClient` returning
  canned JSON, covering valid, malformed then retried, invalid dates and unknown
  cities.
- Flight link golden tests.
- Trip service tests for dates stamping days, stay upsert, flight add/remove and
  version conflicts.
- `ApplyTripAction` tests: owner, expiry, double apply.

**Prod:**

- After each promote, an unauthenticated call to every new RPC returns 401, not
  404.
- Log the extraction success rate on the free chain.

**QA:**

- Web, in the browser: the manual flow and the chat flow ("4-star hotels in
  Lisbon 12–17 Nov" → cards → confirm → editor updates → calendar shows dated
  days).
- iOS: the same, in the simulator, then TestFlight.
- Telegram: the local test bot; tap a button, then check the trip on web.

## Rollout

1. The proto release carries the trip and chat additions: tag, `make push`, check
   the tag contains the RPCs.
2. Server, in PRs: trip service extraction and migration, then flights, then
   actions. `make generate` after the proto bump. Then promote by hand until
   `INFRA_TOKEN` is set.
3. Web.
4. iOS.
5. Telegram.

Each surface ships behind nothing: with no users there is nothing to gate.

## Open risks

- Free models may emit poor JSON often enough that cards rarely appear. The
  extraction success rate metric exists to catch this. The fallback is a paid
  model for extraction only, which is one small call per turn.
- The deep-link formats are unverified.
- This is a supply-side build while the strangers-count is 0 (owner flagged).
