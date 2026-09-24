# Multi-city Trips Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One trip across 2–5 cities on web and iOS. Each city gets its own day plan, hotels, restaurants and activities, with travel legs between the cities and a suggested visiting order.

**Architecture:** One `StreamChat` stream runs the existing single-city pipeline once per city, each as a normal child chat session. A `ROUTE` event goes first, and every per-city event carries a `stop_index`. At the end, one parent Trip is saved with per-day cities, legs and a `cities[]` link to each child session. The route itself comes from the existing pure planner `internal/domain/multicity`.

**Tech Stack:** Protobuf + buf (BSR `loci/loci-proto`), Go 1.x + Connect + pgx + goose, SolidStart + solid-query + vitest, SwiftUI + swift-protobuf + connect-swift + XCTest.

**Spec:** `docs/superpowers/specs/2026-09-24-multi-city-trips-design.md` (this repo, branch `feat/multi-city-trips`).

## Global Constraints

- Recommendations only: no booking, no payment and no new partner integration.
- Cap of 5 cities per trip, enforced in proto (`max_items: 5`) and again on the server. No plan gating.
- Travel mode by straight-line distance: under 100 km `drive`, 100–700 km `train`, over 700 km `flight`. Every duration is an estimate and the UI prefixes it with "≈".
- A stream with no `ROUTE` event is single-city and must keep exactly today's shape. Old clients ignore the new fields.
- A proto change is a release, in two halves. The server needs a tag plus `go get` plus `make generate`. The client needs `make push` (buf push) plus the `@buf/loci_loci-proto.bufbuild_es` bump. Check that the tag actually contains the change.
- iOS builds against the **local** checkout `Loci/loci-connect-proto` (`XCLocalSwiftPackageReference relativePath = "../../loci-connect-proto"`). That checkout must be on a commit with the new generated Swift.
- Migration number: **0101**. Run `scripts/check-migrations.sh`, and renumber if another PR takes 0101 first (guided-onboarding plans one).
- `trip.Repository.SaveTrip` keeps its signature, because guided-onboarding hooks it.
- Shared checkouts: other sessions commit in `loci-client/`, `loci-connect-server/` and `loci-ios/`. **Every repo gets its own worktree off `origin/main`.** Stage explicitly and never `git add -A`. The loci-ios checkout is currently on someone else's branch (`feat/first-run-priming-analytics`).
- Never run repo-wide `pnpm format` in loci-client; format only the paths you touched.
- The pre-commit hook in loci-connect-server runs the whole of `golangci-lint`, which takes about 2 minutes. Let it run.
- **Deviation from spec:** `RoutePayload` carries its own fields (stops, legs, outline, warnings, dropped) and does not embed `compare.v1.MultiCityPlan`. That avoids `chat.proto` importing compare and dragging in `go_score`/`pro_only`. The legs are still `loci.trip.TripLeg`.

## Review Focus

1. **"Lisbon, Porto and Seville" with no duration.** tripspan falls back to a short default and the planner would drop cities for lack of time. Expected: the window is widened to at least 2 days per city, so all three are kept. The test goes in Task 8.
2. **A stop whose generation fails partway through the run.** Expected: the tracker, the resume buffer and the web/iOS reducers treat an `ERROR` with `stop_index` as non-terminal. The other cities still render and the run finishes `done`. Tests go in Tasks 6, 9, 13 and 17.
3. **A 5-city run that needs more than 3 minutes.** Expected: no stop is cancelled by the handler's context deadline. Each city gets its own 3-minute budget inside a 15-minute stream budget. The test goes in Task 6.
4. **A follow-up in an existing Lisbon session: "now add Porto and Seville".** Expected: it starts a new multi-city trip, rather than being read as a request to edit Lisbon or as a single-city Porto trip. The test goes in Task 10.
5. **The same city typed twice ("Lisbon, Porto, Lisbon"), or one that doesn't resolve ("Atlantis").** Expected: duplicates collapse into one stop, and the unresolvable city goes in `dropped` with a reason while the rest continue. If only one real city remains, the request falls back to today's single-city path. Tests go in Tasks 4 and 8.

---

## File map

**loci-connect-proto** (worktree `Loci/loci-connect-proto-multicity`, branch `feat/multi-city`)
- Modify `proto/loci/chat/chat.proto`: `TripStopInput`, `ChatRequest.stops/suggest_order`, `STREAM_EVENT_TYPE_ROUTE`, `StopRef`, `DroppedStop`, `RoutePayload`, `StreamEvent.stop_index`, oneof `route`.
- Modify `proto/loci/trip/trip.proto`: `TripCity`, `TripDraft.cities`.
- Regenerate `gen/` (go, swift).

**loci-connect-server** (worktree `Loci/loci-connect-server-multicity`, branch `feat/multi-city-trips`)
- Modify `internal/domain/multicity/plan.go`: `ModeFor`, `Input.MultiModal`, no-origin support, mode-aware outline.
- Create `internal/domain/multicity/fixed.go` + `fixed_test.go`: `PlanFixed`.
- Create `internal/domain/chat/service/chat_trip_cities.go` + test: `TripCities`, `parseTripCities`, `extractTripCitiesCached`.
- Modify `internal/domain/chat/service/chat_converters.go`: the extractor prompt returns a list of cities.
- Modify `internal/domain/chat/service/chat_process_stream.go`: `extractCityCached` wraps the new extractor; `prepareChatContext` honours preset fields; `persistResults` honours `SuppressTripSave`.
- Modify `internal/domain/chat/common/*.go` (the `ChatContext` file): multi-city fields.
- Modify `internal/types/chat_session.go`: `EventTypeRoute`, `StreamEvent.StopIndex`, `StreamRouteData`.
- Create `internal/domain/chat/service/chat_multicity_route.go` + test: `buildMultiCityRoute`.
- Create `internal/domain/chat/service/chat_multicity.go` + test: dispatch, orchestration, `forwardStopEvent`, `mergeStopTrips`.
- Modify `internal/domain/chat/service/chat_stream_session.go`: `ProcessUnifiedChatMessageStream` dispatches, and the old body becomes `runSingleCity`.
- Modify `internal/domain/chat/service/chat_new_trip.go`: two or more cities means a new trip.
- Modify `internal/domain/chat/handler/chat_handler.go`: stops in, route/stop_index out, terminal rule, stream budget.
- Modify `internal/domain/runs/tracker.go`: a stop-tagged error is not terminal.
- Modify `internal/domain/trip/repository.go`, `mappers.go`: `TripCity`, persisted in `trip_cities`.
- Create `pkg/db/migrations/0101_trip_cities.up.sql`.
- Modify `cmd/api/dependencies.go`: `chatSvc.SetCityResolver(d.CityResolver)`.

**loci-client** (worktree `Loci/loci-client-multicity`, branch `feat/multi-city`)
- Modify `package.json`: bump the BSR package.
- Modify `src/lib/streaming/chatStream.ts`: `route` kind, `stopIndex`, request `stops`.
- Create `src/lib/streaming/multi-city.ts` + test: pure reducers and helpers.
- Modify `src/lib/streaming-service.ts`: per-stop projection.
- Modify `src/lib/api/types.ts`: `StreamingSession.route/stops`.
- Modify `src/lib/hooks/useStreamedRpc.ts`: stops input and store.
- Create `src/components/features/MultiCity/StopBuilder.tsx`, `StopSwitcher.tsx`, `LegRow.tsx`.
- Modify `src/routes/itinerary/index.tsx`, `src/routes/discover.tsx`, `src/routes/hotels/index.tsx`, `restaurants/index.tsx`, `activities/index.tsx`.
- Modify `src/lib/itinerary-offline-store.ts`, `src/lib/saved-itineraries.ts`.

**loci-ios** (worktree `Loci/loci-ios-multicity`, branch `feat/multi-city`)
- Modify `loci/loci/Features/Search/Model/SearchState.swift`: `route`, `stops`, stop-aware `apply`.
- Modify `loci/loci/Features/Search/SearchSessionController.swift`: `start(stops:suggestOrder:)`.
- Modify `loci/loci/Features/Search/Model/SearchEnvelope.swift`: `stops`.
- Create `loci/loci/Features/Search/UI/MultiCity/StopBuilderSheet.swift`, `StopPicker.swift`, `LegRowView.swift`.
- Modify `loci/loci/Features/Search/UI/Results/ResultsPage.swift`.
- Test `loci/lociTests/SearchStateTests.swift`, `loci/lociTests/MultiCityTests.swift`.

---

# Part A — Proto

### Task 1: Multi-city contract

**Files:**
- Modify: `proto/loci/chat/chat.proto`
- Modify: `proto/loci/trip/trip.proto`
- Regenerate: `gen/`

**Interfaces:**
- Produces: `loci.chat.TripStopInput{city_name, nights?, city_id?}`, `ChatRequest.stops (9)`, `ChatRequest.suggest_order (10)`, `StreamEventType.STREAM_EVENT_TYPE_ROUTE = 13`, `StreamEvent.stop_index (11, optional int32)`, `StreamEvent.route (32, RoutePayload)`, `RoutePayload{stops, legs, outline, warnings, dropped, total_travel_mins, trip_id?}`, `StopRef{index, city_name, city_id, session_id, day_numbers}`, `DroppedStop{city_name, reason}`, `loci.trip.TripCity{city_name, city_id, session_id, nights, order_index}`, `TripDraft.cities (13)`.

- [ ] **Step 1: Create the worktree**

```bash
cd ~/Work/production/apps/Loci/loci-connect-proto
git fetch -q origin && git worktree add -b feat/multi-city ../loci-connect-proto-multicity origin/main
cd ../loci-connect-proto-multicity
```

- [ ] **Step 2: Add the trip types** — in `proto/loci/trip/trip.proto`, after `message TripLeg { … }`:

```proto
// TripCity is one city of a multi-city trip, in visiting order, with the chat
// session its places were generated in. Empty for a single-city trip.
message TripCity {
  string city_name = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 200
  }];
  string city_id = 2 [(buf.validate.field).string = {max_len: 100}];
  // The child chat session this city was generated in; reopening the city's
  // hotels, restaurants and activities goes through it.
  string session_id = 3 [(buf.validate.field).string = {max_len: 100}];
  int32 nights = 4 [(buf.validate.field).int32 = {
    gte: 0
    lte: 30
  }];
  int32 order_index = 5 [(buf.validate.field).int32 = {gte: 0}];
}
```

Then, inside `message TripDraft`, after `repeated TripLeg legs = 12;`:

```proto
  // The cities of a multi-city trip, in visiting order. Empty for a
  // single-city trip, whose city is city_name above.
  repeated TripCity cities = 13;
```

Also change the `TripLeg.mode` comment to: `// "drive", "train", "bus" or "flight". An estimate from distance, not a schedule.`

- [ ] **Step 3: Add the chat types** — in `proto/loci/chat/chat.proto`, add `import "loci/trip/trip.proto";` below the existing imports. Add `STREAM_EVENT_TYPE_ROUTE = 13;` as the last value of `enum StreamEventType`. Before `message ChatRequest`:

```proto
// TripStopInput is one city the traveller asked for, in the order given.
message TripStopInput {
  string city_name = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 200
  }];
  // Nights in this city. Absent: the planner decides.
  optional int32 nights = 2 [(buf.validate.field).int32 = {
    gte: 1
    lte: 14
  }];
  optional string city_id = 3 [(buf.validate.field).string = {
    min_len: 1
    max_len: 100
  }];
}
```

Inside `message ChatRequest`, after `request_id = 8`:

```proto
  // A multi-city trip, from the stop builder. Two or more stops skip city
  // extraction; one stop is treated as city_name.
  repeated TripStopInput stops = 9 [(buf.validate.field).repeated = {max_items: 5}];
  // Let the server reorder the stops into a sensible route.
  bool suggest_order = 10;
```

After `message CompletePayload { … }`:

```proto
// StopRef names one city of a multi-city stream: every event about that city
// carries its index in StreamEvent.stop_index.
message StopRef {
  int32 index = 1 [(buf.validate.field).int32 = {gte: 0}];
  string city_name = 2 [(buf.validate.field).string = {max_len: 200}];
  string city_id = 3 [(buf.validate.field).string = {max_len: 100}];
  // The child chat session this city is generated in.
  string session_id = 4 [(buf.validate.field).string = {max_len: 100}];
  // Trip-wide day numbers spent in this city.
  repeated int32 day_numbers = 5;
}

// DroppedStop is a requested city the route left out, and why.
message DroppedStop {
  string city_name = 1 [(buf.validate.field).string = {max_len: 200}];
  string reason = 2 [(buf.validate.field).string = {max_len: 500}];
}

// RoutePayload opens a multi-city stream, before any per-city event, and is
// sent again once the parent trip is saved, with trip_id set.
message RoutePayload {
  repeated StopRef stops = 1;
  // Travel between consecutive cities, in trip order.
  repeated loci.trip.TripLeg legs = 2;
  string outline = 3 [(buf.validate.field).string = {max_len: 2000}];
  repeated string warnings = 4;
  repeated DroppedStop dropped = 5;
  int32 total_travel_mins = 6 [(buf.validate.field).int32 = {gte: 0}];
  optional string trip_id = 7 [(buf.validate.field).string = {max_len: 100}];
}
```

Inside `message StreamEvent`, after `request_id = 10`:

```proto
  // Set on every per-city event of a multi-city stream: the StopRef.index it
  // belongs to. An ERROR carrying it failed that city only, not the stream.
  optional int32 stop_index = 11;
```

In the `oneof payload`, after `CompletePayload complete = 31;`: `RoutePayload route = 32;`

- [ ] **Step 4: Lint and generate**

Run: `make lint && buf breaking --against '.git#branch=origin/main' && make generate`
Expected: no lint errors, **no breaking changes** (every change is additive), and `gen/go/loci/chat/chat.pb.go` contains `StopIndex` and `RoutePayload`. Check the headers of the generated files: they must name the `protoc-gen-go` version pinned in `buf.gen.yaml`.

- [ ] **Step 5: Commit, PR and merge**

```bash
git add proto/loci/chat/chat.proto proto/loci/trip/trip.proto gen/
git commit -m "feat(chat,trip): multi-city trips — stops, route event, stop_index, trip cities"
git push -u origin feat/multi-city && gh pr create --fill
```

After merge: `git switch main && git pull && make release VERSION=v5.26.0`. Confirm with `git tag --contains <merge-sha> | grep v5.26.0` and check the BSR commit id printed by `make push`. Then update the shared local checkout, which iOS builds against: `cd ../loci-connect-proto && git pull --ff-only` (only if it is on `main` and clean; otherwise ask).

---

# Part B — Server

All steps run in the worktree `~/Work/production/apps/Loci/loci-connect-server-multicity` (branch `feat/multi-city-trips`, which already holds the spec). Rebase it onto `origin/main` first: `git fetch -q && git rebase origin/main`.

### Task 2: Take proto v5.26.0

**Files:** Modify `go.mod`, `go.sum`, `gen/` (if the server vendors generated code)

- [ ] **Step 1:** `go get github.com/FACorreiaa/loci-connect-proto/v5@v5.26.0 && go mod tidy`
- [ ] **Step 2:** `make generate`. Memory says the server's `gen/` has to be regenerated after every proto bump, or CI goes red and CD skips silently.
- [ ] **Step 3:** `go build ./... && go test ./internal/domain/chat/... ./internal/domain/trip/...`. Expected: PASS, with no behaviour change yet.
- [ ] **Step 4:** Commit: `git add go.mod go.sum gen/ && git commit -m "chore: proto v5.26.0 (multi-city contract)"`

### Task 3: Multi-modal legs and no-origin routes in `multicity`

**Files:**
- Modify: `internal/domain/multicity/plan.go`
- Test: `internal/domain/multicity/plan_test.go`

**Interfaces:**
- Produces: `func ModeFor(distanceKm float64) (mode string, mins int)`, `Input.MultiModal bool`. `Input.OriginName == ""` means no origin: no outbound leg, and ordering starts from the first candidate as given.

- [ ] **Step 1: Write the failing tests** (append to `plan_test.go`)

```go
var rome = City{ID: "rome", Name: "Rome", Lat: 41.90, Lon: 12.50, Score: 88, POICount: 40}

func TestModeFor(t *testing.T) {
	cases := []struct {
		km   float64
		mode string
	}{{40, "drive"}, {99, "drive"}, {100, "train"}, {313, "train"}, {700, "train"}, {701, "flight"}, {1860, "flight"}}
	for _, c := range cases {
		mode, mins := ModeFor(c.km)
		if mode != c.mode {
			t.Errorf("ModeFor(%v) mode = %q, want %q", c.km, mode, c.mode)
		}
		if mins <= 0 {
			t.Errorf("ModeFor(%v) mins = %d, want > 0", c.km, mins)
		}
	}
	// A flight to Rome must be faster door-to-door than driving it.
	_, fly := ModeFor(1860)
	if drive := geo.DriveMins(1860); fly >= drive {
		t.Errorf("flight %d min should beat drive %d min", fly, drive)
	}
}

// Without MultiModal, Lisbon→Rome is a 20-hour drive and gets dropped; with it,
// it is a flight and the city stays.
func TestPlan_MultiModalKeepsFarCity(t *testing.T) {
	start, end := window(6 * 24)
	in := Input{Candidates: []City{lisbon, rome}, Start: start, End: end}

	if got := Plan(in); len(got.Cities) != 1 {
		t.Fatalf("drive-only: expected Rome dropped, got %v", cityNames(got.Cities))
	}
	in.MultiModal = true
	got := Plan(in)
	if len(got.Cities) != 2 {
		t.Fatalf("multi-modal: expected both cities, got %v (dropped %v)", cityNames(got.Cities), got.Dropped)
	}
	if len(got.Legs) != 1 || got.Legs[0].Mode != "flight" {
		t.Fatalf("expected one flight leg and no outbound leg, got %+v", got.Legs)
	}
	if !strings.Contains(got.Outline, "travel") || strings.Contains(got.Outline, "driving") {
		t.Errorf("outline should talk about travel, not driving: %q", got.Outline)
	}
}

// No origin: ordering starts at the first city named, and there is no leg
// from nowhere.
func TestPlan_NoOriginStartsAtFirstCandidate(t *testing.T) {
	start, end := window(6 * 24)
	got := Plan(Input{Candidates: []City{porto, lisbon, coimbra}, Start: start, End: end, MultiModal: true})
	if names := cityNames(got.Cities); len(names) != 3 || names[0] != "Porto" {
		t.Fatalf("expected route to start at Porto, got %v", names)
	}
	for _, l := range got.Legs {
		if l.FromName == "" {
			t.Fatalf("unexpected outbound leg from no origin: %+v", l)
		}
	}
}
```

Add `"github.com/FACorreiaa/loci-connect-api/pkg/geo"` to the test file's imports.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/domain/multicity/ -run 'ModeFor|MultiModal|NoOrigin' -v`
Expected: FAIL, `undefined: ModeFor` / `unknown field MultiModal`.

- [ ] **Step 3: Implement** in `plan.go`:

Add to `Input`:

```go
	// MultiModal lets legs be trains and flights, picked by distance. Off,
	// every leg is a drive — the weekend comparison's original assumption,
	// kept so its answers do not change.
	MultiModal bool
```

Add below the constants:

```go
// ModeFor picks how a traveller would plausibly cover a straight-line
// distance, and roughly how long it takes door to door. It is an estimate for
// planning, not a schedule: the UI says "≈".
func ModeFor(distanceKm float64) (mode string, mins int) {
	switch {
	case distanceKm < 100:
		return "drive", geo.DriveMins(distanceKm)
	case distanceKm <= 700:
		// ~100 km/h average including stops, plus getting to and from stations.
		return "train", int(distanceKm/100*60) + 30
	default:
		// ~700 km/h in the air, plus three hours of airports.
		return "flight", int(distanceKm/700*60) + 180
	}
}

// legTravel is the mode and minutes for one leg under this input's rules.
func (in Input) legTravel(km float64) (string, int) {
	if in.MultiModal {
		return ModeFor(km)
	}
	return "drive", geo.DriveMins(km)
}

// hasOrigin reports whether the trip starts somewhere other than its first city.
func (in Input) hasOrigin() bool { return in.OriginName != "" }
```

In `Plan`, right after `if len(in.Candidates) == 0 { … }`, insert:

```go
	// No origin: the trip starts in the first city the traveller named, so
	// order from there and charge no outbound leg.
	if !in.hasOrigin() {
		in.OriginLat, in.OriginLon = in.Candidates[0].Lat, in.Candidates[0].Lon
		in.ReturnToOrigin = false
	}
```

In the `for _, c := range ordered` loop, replace both `geo.DriveMins(geo.HaversineKm(…))` calls with `in.legTravel(geo.HaversineKm(…))`, keeping only the minutes (`_, legMins := …`, `_, back := …`). Do the same for the final return-to-origin addition. Because `routeOrder` measures from `(OriginLat, OriginLon)`, which is now the first candidate, that candidate is picked first at distance 0 and costs 0 minutes.

In `buildLegs`, replace the `leg` closure's body so it uses `mode, mins := in.legTravel(km)` with `DurationMins: mins, Mode: mode`. Wrap the outbound append in `if in.hasOrigin() { … }`.

Change `outline`'s signature to `outline(cities []City, days []DayPlan, travelMins int, multiModal bool) string`, and its last line to:

```go
	if multiModal {
		return fmt.Sprintf("%s · ≈%s travel in total", joined, humanMins(travelMins))
	}
	return fmt.Sprintf("%s · %s driving in total", joined, humanMins(travelMins))
```

Update the call in `Plan` to `outline(chosen, out.Days, travelMins, in.MultiModal)`.

- [ ] **Step 4: Run all multicity and compare tests**

Run: `go test ./internal/domain/multicity/ ./internal/domain/compare/ -v`
Expected: PASS, including every existing test. Compare never sets `MultiModal` and always passes an origin.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/multicity/plan.go internal/domain/multicity/plan_test.go
git commit -m "feat(multicity): train/flight legs by distance and routes without an origin"
```

### Task 4: User-ordered routes (`PlanFixed`)

**Files:**
- Create: `internal/domain/multicity/fixed.go`
- Test: `internal/domain/multicity/fixed_test.go`

**Interfaces:**
- Consumes: `Input`, `City`, `Route`, `buildLegs`, `warnings`, `outline` (Task 3).
- Produces: `type FixedStop struct{ City City; Nights int }`, `func PlanFixed(in Input, stops []FixedStop) Route`. The traveller's order and nights are kept as given. Each city gets `Nights` days, and anything below 1 counts as 1. Duplicate city names collapse into the first occurrence, with the nights added together.

- [ ] **Step 1: Write the failing test** (`fixed_test.go`)

```go
package multicity

import "testing"

func TestPlanFixed_KeepsOrderAndNights(t *testing.T) {
	got := PlanFixed(Input{MultiModal: true}, []FixedStop{{City: lisbon, Nights: 3}, {City: porto, Nights: 2}})

	if !got.Feasible || len(got.Cities) != 2 || got.Cities[0].Name != "Lisbon" {
		t.Fatalf("expected Lisbon then Porto, got %v", cityNames(got.Cities))
	}
	if len(got.Days) != 5 {
		t.Fatalf("expected 5 days, got %d", len(got.Days))
	}
	want := []string{"Lisbon", "Lisbon", "Lisbon", "Porto", "Porto"}
	for i, d := range got.Days {
		if d.CityName != want[i] || d.DayNumber != i+1 {
			t.Errorf("day %d = %s/%d, want %s/%d", i, d.CityName, d.DayNumber, want[i], i+1)
		}
	}
	if !got.Days[3].TravelDay || got.Days[1].TravelDay {
		t.Errorf("day 4 (arrive Porto) should be the only travel day after day 1")
	}
	if len(got.Legs) != 1 || got.Legs[0].AfterDay != 3 || got.Legs[0].Mode != "train" {
		t.Fatalf("expected one train leg after day 3, got %+v", got.Legs)
	}
}

func TestPlanFixed_DuplicatesCollapse(t *testing.T) {
	got := PlanFixed(Input{MultiModal: true}, []FixedStop{
		{City: lisbon, Nights: 2}, {City: porto, Nights: 1}, {City: lisbon, Nights: 1},
	})
	if len(got.Cities) != 2 {
		t.Fatalf("expected duplicates collapsed, got %v", cityNames(got.Cities))
	}
	if n := countDays(got.Days, "Lisbon"); n != 3 {
		t.Errorf("Lisbon should keep the combined 3 nights, got %d", n)
	}
}

func TestPlanFixed_ZeroNightsIsOne(t *testing.T) {
	got := PlanFixed(Input{MultiModal: true}, []FixedStop{{City: lisbon}, {City: porto, Nights: -2}})
	if len(got.Days) != 2 {
		t.Fatalf("expected one day per city, got %d", len(got.Days))
	}
}

func countDays(days []DayPlan, city string) int {
	n := 0
	for _, d := range days {
		if d.CityName == city {
			n++
		}
	}
	return n
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/domain/multicity/ -run PlanFixed -v`
Expected: FAIL, `undefined: PlanFixed`.

- [ ] **Step 3: Implement** (`fixed.go`)

```go
package multicity

import "strings"

// FixedStop is a city the traveller placed themselves, with their own nights.
type FixedStop struct {
	City   City
	Nights int
}

// PlanFixed lays out a route the traveller already decided: their order,
// their nights. Nothing is dropped or reordered — Plan is for when they want
// a suggestion — but legs, travel days, warnings and the outline are computed
// exactly as Plan computes them, so the two routes read the same.
func PlanFixed(in Input, stops []FixedStop) Route {
	merged := make([]FixedStop, 0, len(stops))
	index := map[string]int{}
	for _, s := range stops {
		if s.Nights < 1 {
			s.Nights = 1
		}
		key := strings.ToLower(strings.TrimSpace(s.City.Name))
		if i, ok := index[key]; ok {
			merged[i].Nights += s.Nights
			continue
		}
		index[key] = len(merged)
		merged = append(merged, s)
	}
	if len(merged) == 0 {
		return Route{Outline: "No candidate cities to plan."}
	}

	cities := make([]City, len(merged))
	var days []DayPlan
	dayNum := 1
	for i, s := range merged {
		cities[i] = s.City
		for d := 0; d < s.Nights; d++ {
			plan := DayPlan{
				DayNumber: dayNum,
				CityName:  s.City.Name,
				CityID:    s.City.ID,
				Lat:       s.City.Lat,
				Lon:       s.City.Lon,
				TravelDay: d == 0 && (i > 0 || in.hasOrigin()),
			}
			if !in.Start.IsZero() {
				date := in.Start.AddDate(0, 0, dayNum-1)
				plan.Date = &date
			}
			days = append(days, plan)
			dayNum++
		}
	}

	legs := buildLegs(in, cities, days)
	travelMins := 0
	for _, l := range legs {
		travelMins += l.DurationMins
	}
	windowMins := float64(len(days)) * 24 * 60
	share := float64(travelMins) / windowMins
	if share > 1 {
		share = 1
	}

	return Route{
		Feasible:        true,
		Cities:          cities,
		Days:            days,
		Legs:            legs,
		TotalTravelMins: travelMins,
		TravelShare:     share,
		Warnings:        warnings(cities, days, share),
		Outline:         outline(cities, days, travelMins, in.MultiModal),
	}
}

```

- [ ] **Step 4: Run**

Run: `go test ./internal/domain/multicity/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/multicity/fixed.go internal/domain/multicity/fixed_test.go
git commit -m "feat(multicity): PlanFixed keeps the traveller's order and nights"
```

### Task 5: The extractor returns every city

**Files:**
- Create: `internal/domain/chat/service/chat_trip_cities.go`
- Test: `internal/domain/chat/service/chat_trip_cities_test.go`
- Modify: `internal/domain/chat/service/chat_converters.go:30-96` (`extractCityFromMessage`)
- Modify: `internal/domain/chat/service/chat_process_stream.go:224-247` (`extractCityCached`)

**Interfaces:**
- Produces:
  ```go
  type ExtractedCity struct { Name string `json:"city"`; Days int `json:"days,omitempty"` }
  type TripCities struct { Cities []ExtractedCity `json:"cities"`; Ordered bool `json:"ordered"`; Message string `json:"message"` }
  func parseTripCities(raw, original string) (TripCities, error)
  func (l *ServiceImpl) extractTripCitiesCached(ctx context.Context, message string) (TripCities, error)
  ```
  `extractCityCached` keeps its signature and returns `Cities[0].Name` (or "") and `Message`.

- [ ] **Step 1: Write the failing test** (`chat_trip_cities_test.go`)

```go
package service

import "testing"

func TestParseTripCities(t *testing.T) {
	cases := []struct {
		name, raw   string
		wantCities  []string
		wantDays    []int
		wantOrdered bool
		wantMsg     string
	}{
		{"ordered with days",
			`{"cities":[{"city":"Lisbon","days":3},{"city":"Porto","days":2}],"ordered":true,"message":"itinerary"}`,
			[]string{"Lisbon", "Porto"}, []int{3, 2}, true, "itinerary"},
		{"unordered",
			"```json\n{\"cities\":[{\"city\":\"Lisbon\"},{\"city\":\"Porto\"},{\"city\":\"Seville\"}],\"ordered\":false,\"message\":\"a week trip\"}\n```",
			[]string{"Lisbon", "Porto", "Seville"}, []int{0, 0, 0}, false, "a week trip"},
		{"legacy single-city shape",
			`{"city":"Barcelona","message":"Find restaurants"}`,
			[]string{"Barcelona"}, []int{0}, false, "Find restaurants"},
		{"duplicates and blanks collapse",
			`{"cities":[{"city":"Lisbon","days":2},{"city":" lisbon "},{"city":""},{"city":"Porto"}],"ordered":true,"message":"trip"}`,
			[]string{"Lisbon", "Porto"}, []int{2, 0}, true, "trip"},
		{"no city keeps the original message",
			`{"cities":[],"ordered":false,"message":""}`,
			nil, nil, false, "ORIGINAL"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseTripCities(c.raw, "ORIGINAL")
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Cities) != len(c.wantCities) {
				t.Fatalf("cities = %+v, want %v", got.Cities, c.wantCities)
			}
			for i := range c.wantCities {
				if got.Cities[i].Name != c.wantCities[i] || got.Cities[i].Days != c.wantDays[i] {
					t.Errorf("city %d = %+v, want %s/%d", i, got.Cities[i], c.wantCities[i], c.wantDays[i])
				}
			}
			if got.Ordered != c.wantOrdered || got.Message != c.wantMsg {
				t.Errorf("ordered/message = %v/%q, want %v/%q", got.Ordered, got.Message, c.wantOrdered, c.wantMsg)
			}
		})
	}
}

func TestParseTripCities_Garbage(t *testing.T) {
	if _, err := parseTripCities("not json", "x"); err == nil {
		t.Fatal("expected an error for unparseable output")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/domain/chat/service/ -run ParseTripCities -v`
Expected: FAIL, `undefined: parseTripCities`.

- [ ] **Step 3: Implement** (`chat_trip_cities.go`)

```go
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/lib" // same import chat_converters.go uses for CleanJSON — copy its exact path
)

// ExtractedCity is one city read out of a request, with the days the
// traveller gave it (0 when they did not say).
type ExtractedCity struct {
	Name string `json:"city"`
	Days int    `json:"days,omitempty"`
}

// TripCities is everything the extractor reads out of one message. One city is
// today's single-city request; two or more is a multi-city trip. Ordered says
// the traveller sequenced them themselves ("then", "after that", "finishing
// in") rather than listing candidates.
type TripCities struct {
	Cities  []ExtractedCity `json:"cities"`
	Ordered bool            `json:"ordered"`
	Message string          `json:"message"`
}

// First is the single city a single-city caller wants, or "".
func (t TripCities) First() string {
	if len(t.Cities) == 0 {
		return ""
	}
	return t.Cities[0].Name
}

// parseTripCities reads the extractor's JSON. It also accepts the old
// {"city","message"} shape, because the extraction cache holds entries written
// before this change for up to a day.
func parseTripCities(raw, original string) (TripCities, error) {
	clean := generativeAI.CleanJSON(raw)
	var parsed struct {
		TripCities
		City string `json:"city"`
	}
	if err := json.Unmarshal([]byte(clean), &parsed); err != nil {
		return TripCities{}, fmt.Errorf("failed to parse extraction response: %w", err)
	}
	out := TripCities{Ordered: parsed.Ordered, Message: parsed.Message}
	in := parsed.Cities
	if len(in) == 0 && strings.TrimSpace(parsed.City) != "" {
		in = []ExtractedCity{{Name: parsed.City}}
	}
	seen := map[string]bool{}
	for _, c := range in {
		name := strings.TrimSpace(c.Name)
		key := strings.ToLower(name)
		if name == "" || seen[key] {
			continue
		}
		seen[key] = true
		if c.Days < 0 {
			c.Days = 0
		}
		out.Cities = append(out.Cities, ExtractedCity{Name: name, Days: c.Days})
	}
	if len(out.Cities) == 0 || strings.TrimSpace(out.Message) == "" {
		out.Message = original
	}
	return out, nil
}

// tripCitiesCacheKey is the extraction cache key for the list-shaped answer.
// A new prefix, so entries from the single-city extractor are not misread.
func tripCitiesCacheKey(message string) string { return "tripcities:" + extractionCacheKey(message) }

// extractTripCitiesCached is extractCityCached for the whole list: one
// provider call per distinct message, shared by everyone, for a day.
func (l *ServiceImpl) extractTripCitiesCached(ctx context.Context, message string) (TripCities, error) {
	key := tripCitiesCacheKey(message)
	if raw, ok := l.cachedText(key); ok {
		var tc TripCities
		if json.Unmarshal([]byte(raw), &tc) == nil {
			return tc, nil
		}
	}
	tc, err := l.extractTripCitiesFromMessage(ctx, message)
	if err != nil {
		return TripCities{}, err
	}
	if raw, mErr := json.Marshal(tc); mErr == nil && l.cache != nil {
		l.cache.Set(key, string(raw), extractionCacheTTL)
	}
	return tc, nil
}
```

Before writing it, check the real `generativeAI` import path in `chat_converters.go` and use exactly that one.

In `chat_converters.go`, rename `extractCityFromMessage` to `extractTripCitiesFromMessage(ctx, message) (TripCities, error)`. Replace its prompt with:

```go
	prompt := fmt.Sprintf(`
You are a text parser. Read every city the traveller wants to visit, in the order they gave them, and return a clean version of the message with the city names and per-city durations removed.

User message: "%s"

Respond with ONLY a JSON object in this exact format:
{
    "cities": [{"city": "City Name", "days": 0}],
    "ordered": false,
    "message": "cleaned message without cities"
}

"days" is how many days or nights the traveller gave that one city, or 0 if they did not say.
"ordered" is true only when they sequenced the cities themselves ("then", "after", "ending in", "first ... then").
A neighbourhood, landmark or region is not a city. Never invent a city.

Examples:
- "Find restaurants in Barcelona" → {"cities":[{"city":"Barcelona","days":0}],"ordered":false,"message":"Find restaurants"}
- "Lisbon for 3 days then Porto for 2" → {"cities":[{"city":"Lisbon","days":3},{"city":"Porto","days":2}],"ordered":true,"message":"trip"}
- "A week in Lisbon, Porto and Seville" → {"cities":[{"city":"Lisbon","days":0},{"city":"Porto","days":0},{"city":"Seville","days":0}],"ordered":false,"message":"A week trip"}
- "What to do in Paris?" → {"cities":[{"city":"Paris","days":0}],"ordered":false,"message":"What to do"}
- "Replace Alfama with Belém" → {"cities":[],"ordered":false,"message":"Replace Alfama with Belém"}

If no city is mentioned, return an empty "cities" list.
`, message)
```

Keep the LLM call exactly as it is. Replace the tail, from `cleanResponse := …` to the end, with `return parseTripCities(responseText.String(), message)`, and the early returns with `return TripCities{}, err` equivalents.

In `chat_process_stream.go`, replace the body of `extractCityCached`:

```go
func (l *ServiceImpl) extractCityCached(ctx context.Context, message string) (cityName, cleanedMessage string, err error) {
	tc, err := l.extractTripCitiesCached(ctx, message)
	if err != nil {
		return "", "", err
	}
	return tc.First(), tc.Message, nil
}
```

Keep its doc comment, and add one line: "It is the single-city view of extractTripCitiesCached, so both share one provider call."

- [ ] **Step 4: Run**

Run: `go test ./internal/domain/chat/... -v -run 'ParseTripCities|Extract|NewTrip|StartsNewTrip'`
Then: `go test ./internal/domain/chat/...`
Expected: PASS. Existing tests that stub `extractCityFromMessage` must be updated to stub `extractTripCitiesFromMessage`; grep `_test.go` for the old name.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/chat/service/chat_trip_cities.go internal/domain/chat/service/chat_trip_cities_test.go \
  internal/domain/chat/service/chat_converters.go internal/domain/chat/service/chat_process_stream.go
git commit -m "feat(chat): city extractor reads every city, in order, with per-city days"
```

### Task 6: Stream plumbing — stop_index, ROUTE, terminal rule, budget

**Files:**
- Modify: `internal/types/chat_session.go` (StreamEvent, constants, new data type)
- Modify: `internal/domain/chat/handler/chat_handler.go` (request → cc, `mapEventToProto`, terminal check, budget)
- Modify: `internal/domain/runs/tracker.go:45-61`
- Modify: `internal/domain/chat/common/<file with ChatContext>` (fields)
- Test: `internal/domain/chat/handler/chat_multicity_handler_test.go`, `internal/domain/runs/tracker_test.go`

**Interfaces:**
- Produces:
  ```go
  // types
  const EventTypeRoute = "route"
  // StreamEvent gains:  StopIndex *int `json:"stop_index,omitempty"`
  type StreamRouteStop struct { Index int; CityName, CityID, SessionID string; DayNumbers []int }
  type StreamRouteLeg struct { AfterDay int; FromName, ToName string; FromLat, FromLon, ToLat, ToLon, DistanceKm float64; DurationMins int; Mode string }
  type StreamRouteData struct { Stops []StreamRouteStop; Legs []StreamRouteLeg; Outline string; Warnings []string; Dropped []StreamDroppedStop; TotalTravelMins int; TripID string }
  type StreamDroppedStop struct { CityName, Reason string }
  func (e StreamEvent) IsTerminal() bool   // COMPLETE, or ERROR without StopIndex
  // common.ChatContext gains:
  //   Stops []TripStopRequest; SuggestOrder bool     (from the request)
  //   StopRun bool; PresetSessionID uuid.UUID; PresetTripDays int; SuppressTripSave bool   (set by the orchestrator on child runs)
  type TripStopRequest struct { CityName string; Nights int; CityID string }   // in package common
  // handler
  const perCityBudget = 3 * time.Minute; const maxStops = 5
  ```

- [ ] **Step 1: Write the failing tests**

`internal/domain/chat/handler/chat_multicity_handler_test.go`:

```go
package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func intPtr(i int) *int { return &i }

func TestStreamEvent_IsTerminal(t *testing.T) {
	cases := []struct {
		ev   locitypes.StreamEvent
		want bool
	}{
		{locitypes.StreamEvent{Type: locitypes.EventTypeComplete}, true},
		{locitypes.StreamEvent{Type: locitypes.EventTypeError}, true},
		{locitypes.StreamEvent{Type: locitypes.EventTypeError, StopIndex: intPtr(1)}, false},
		{locitypes.StreamEvent{Type: locitypes.EventTypeItinerary, StopIndex: intPtr(0)}, false},
		{locitypes.StreamEvent{Type: locitypes.EventTypeRoute}, false},
	}
	for _, c := range cases {
		if got := c.ev.IsTerminal(); got != c.want {
			t.Errorf("%s stop=%v: IsTerminal = %v, want %v", c.ev.Type, c.ev.StopIndex, got, c.want)
		}
	}
}

func TestMapEventToProto_RouteAndStopIndex(t *testing.T) {
	h := &ChatHandler{}
	ev := locitypes.StreamEvent{
		Type: locitypes.EventTypeRoute,
		Data: locitypes.StreamRouteData{
			Stops: []locitypes.StreamRouteStop{
				{Index: 0, CityName: "Lisbon", SessionID: "s0", DayNumbers: []int{1, 2, 3}},
				{Index: 1, CityName: "Porto", SessionID: "s1", DayNumbers: []int{4, 5}},
			},
			Legs:    []locitypes.StreamRouteLeg{{AfterDay: 3, FromName: "Lisbon", ToName: "Porto", DistanceKm: 274, DurationMins: 194, Mode: "train"}},
			Outline: "Lisbon (3 days) → Porto (2 days) · ≈3h14 travel in total",
			TripID:  "trip-1",
		},
	}
	got, err := h.mapEventToProto(context.Background(), ev, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if got.GetEventType() != chatv1.StreamEventType_STREAM_EVENT_TYPE_ROUTE {
		t.Fatalf("event type = %v", got.GetEventType())
	}
	r := got.GetRoute()
	if len(r.GetStops()) != 2 || r.GetStops()[1].GetSessionId() != "s1" || len(r.GetStops()[1].GetDayNumbers()) != 2 {
		t.Fatalf("stops = %+v", r.GetStops())
	}
	if len(r.GetLegs()) != 1 || r.GetLegs()[0].GetMode() != "train" || r.GetTripId() != "trip-1" {
		t.Fatalf("legs/trip = %+v %q", r.GetLegs(), r.GetTripId())
	}

	tagged := locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: "boom", StopIndex: intPtr(1)}
	p, err := h.mapEventToProto(context.Background(), tagged, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if !p.HasStopIndex() || p.GetStopIndex() != 1 {
		t.Fatalf("stop_index not carried: %+v", p)
	}
}

func TestStreamBudget(t *testing.T) {
	if streamBudget() < 5*perCityBudget {
		t.Fatalf("stream budget %v must cover %d sequential cities", streamBudget(), maxStops)
	}
}
```

(`HasStopIndex` is generated for `optional` fields in protobuf-go opaque/hybrid APIs. If the generated code uses open structs instead, assert `p.StopIndex != nil && *p.StopIndex == 1`. Match whatever `gen/` produced for `optional string request_id`.)

`internal/domain/runs/tracker_test.go`: add

```go
func TestTracker_StopErrorIsNotTerminal(t *testing.T) {
	store := newFakeRunStore() // reuse the fake this file already uses; grep "func new" in tracker_test.go
	tr := NewTracker(store, "run-1", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	one := 1
	tr.Observe(locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: "porto failed", StopIndex: &one})
	if store.finished("run-1") {
		t.Fatal("a stop-tagged error must not finish the run")
	}
	tr.Observe(locitypes.StreamEvent{Type: locitypes.EventTypeComplete})
	if got := store.status("run-1"); got != StatusDone {
		t.Fatalf("status = %v, want done", got)
	}
}
```

If `tracker_test.go` has no fake store, build one against the interface `NewTracker` takes. Check its signature first; the test must not touch the database.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/domain/chat/handler/ ./internal/domain/runs/ -run 'IsTerminal|RouteAndStopIndex|StreamBudget|StopErrorIsNotTerminal' -v`
Expected: FAIL, undefined `StopIndex`, `EventTypeRoute`, `IsTerminal`, `streamBudget`.

- [ ] **Step 3: Implement**

`internal/types/chat_session.go`: add `EventTypeRoute = "route"` to the event-type const block. Add to `StreamEvent`:

```go
	// StopIndex is set on every per-city event of a multi-city stream: the
	// index of the city it belongs to. An error carrying it failed that city
	// only; the stream goes on to the next one.
	StopIndex *int `json:"stop_index,omitempty"`
```

Then add:

```go
// IsTerminal reports whether this event ends the stream: COMPLETE, or an
// ERROR that is not about one city of a multi-city trip.
func (e StreamEvent) IsTerminal() bool {
	switch e.Type {
	case EventTypeComplete:
		return true
	case EventTypeError:
		return e.StopIndex == nil
	}
	return false
}

// StreamRouteData opens a multi-city stream (see chat.proto RoutePayload).
type StreamRouteData struct {
	Stops           []StreamRouteStop   `json:"stops"`
	Legs            []StreamRouteLeg    `json:"legs"`
	Outline         string              `json:"outline"`
	Warnings        []string            `json:"warnings,omitempty"`
	Dropped         []StreamDroppedStop `json:"dropped,omitempty"`
	TotalTravelMins int                 `json:"total_travel_mins"`
	TripID          string              `json:"trip_id,omitempty"`
}

type StreamRouteStop struct {
	Index      int    `json:"index"`
	CityName   string `json:"city_name"`
	CityID     string `json:"city_id,omitempty"`
	SessionID  string `json:"session_id"`
	DayNumbers []int  `json:"day_numbers"`
}

type StreamRouteLeg struct {
	AfterDay     int     `json:"after_day"`
	FromName     string  `json:"from_name"`
	ToName       string  `json:"to_name"`
	FromLat      float64 `json:"from_lat"`
	FromLon      float64 `json:"from_lon"`
	ToLat        float64 `json:"to_lat"`
	ToLon        float64 `json:"to_lon"`
	DistanceKm   float64 `json:"distance_km"`
	DurationMins int     `json:"duration_mins"`
	Mode         string  `json:"mode"`
}

type StreamDroppedStop struct {
	CityName string `json:"city_name"`
	Reason   string `json:"reason"`
}
```

`common.ChatContext`: add, after `TripID`:

```go
	// Stops is a multi-city trip from the stop builder (ChatRequest.stops).
	// Two or more skip city extraction.
	Stops []TripStopRequest
	// SuggestOrder lets the planner reorder Stops.
	SuggestOrder bool

	// The fields below are set by the multi-city orchestrator on the child run
	// it makes for each city; a request never sets them.

	// StopRun marks a child run: it never dispatches to multi-city again.
	StopRun bool
	// PresetSessionID is the session id the child run creates (the route
	// named it before the city started generating).
	PresetSessionID uuid.UUID
	// PresetTripDays replaces the day count read from the message.
	PresetTripDays int
	// SuppressTripSave stops the child saving its own Trip: the parent trip
	// holds every city.
	SuppressTripSave bool
```

Then add:

```go
// TripStopRequest is one city of a multi-city request.
type TripStopRequest struct {
	CityName string
	Nights   int
	CityID   string
}
```

`runs/tracker.go` `Observe`: change `case locitypes.EventTypeError:` to

```go
	case locitypes.EventTypeError:
		if ev.StopIndex != nil {
			// One city of a multi-city trip failed; the run goes on.
			return
		}
```

(and keep the existing body after it).

`chat_handler.go`:
1. Add

```go
// perCityBudget bounds one city's generation — what the whole stream used to
// get. A multi-city stream runs its cities one after another, so the stream
// as a whole gets one budget per possible city; runSingleCity applies the
// per-city bound itself.
const (
	perCityBudget = 3 * time.Minute
	maxStops      = 5
)

func streamBudget() time.Duration { return perCityBudget * maxStops }
```

2. Replace `context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)` with `context.WithTimeout(context.WithoutCancel(ctx), streamBudget())`, and update the comment above it.
3. Before building `cc`:

```go
	var stops []common.TripStopRequest
	for _, s := range req.Msg.GetStops() {
		stops = append(stops, common.TripStopRequest{CityName: s.GetCityName(), Nights: int(s.GetNights()), CityID: s.GetCityId()})
	}
	// One stop is just a city.
	if len(stops) == 1 && cityName == "" {
		cityName = stops[0].CityName
		stops = nil
	}
```

Then set `Stops: stops, SuggestOrder: req.Msg.GetSuggestOrder()` in the `cc` literal.
4. In the event loop, replace `if event.Type == locitypes.EventTypeComplete || event.Type == locitypes.EventTypeError {` with `if event.IsTerminal() {`.
5. In `mapEventToProto`, after building `resp`:

```go
	if event.StopIndex != nil {
		idx := int32(*event.StopIndex)
		resp.StopIndex = &idx // or resp.SetStopIndex(idx) — match the generated API
	}
```

Add a case:

```go
	case locitypes.EventTypeRoute:
		var rd locitypes.StreamRouteData
		decodeData(event.Data, &rd)
		resp.Payload = &chatv1.StreamEvent_Route{Route: routeToProto(rd)}
```

and the helper (same file):

```go
func routeToProto(rd locitypes.StreamRouteData) *chatv1.RoutePayload {
	out := &chatv1.RoutePayload{
		Outline:         rd.Outline,
		Warnings:        rd.Warnings,
		TotalTravelMins: int32(rd.TotalTravelMins),
	}
	if rd.TripID != "" {
		out.TripId = &rd.TripID
	}
	for _, s := range rd.Stops {
		days := make([]int32, len(s.DayNumbers))
		for i, d := range s.DayNumbers {
			days[i] = int32(d)
		}
		out.Stops = append(out.Stops, &chatv1.StopRef{
			Index: int32(s.Index), CityName: s.CityName, CityId: s.CityID, SessionId: s.SessionID, DayNumbers: days,
		})
	}
	for _, l := range rd.Legs {
		out.Legs = append(out.Legs, &tripv1.TripLeg{
			AfterDay: int32(l.AfterDay), FromName: l.FromName, ToName: l.ToName,
			FromLat: l.FromLat, FromLon: l.FromLon, ToLat: l.ToLat, ToLon: l.ToLon,
			DistanceKm: l.DistanceKm, DurationMins: int32(l.DurationMins), Mode: l.Mode,
		})
	}
	for _, d := range rd.Dropped {
		out.Dropped = append(out.Dropped, &chatv1.DroppedStop{CityName: d.CityName, Reason: d.Reason})
	}
	return out
}
```

Check the `tripv1.TripLeg` field types in `gen/`. `FromLat` etc. may be `optional` pointers, in which case copy `internal/domain/compare/multicity.go` `toTripLegProtos`, which already builds that message. Better still: move `toTripLegProtos` into a shared helper in `internal/domain/trip` and call it from both places.

6. `eventTypeToProto`: map `EventTypeRoute` to `STREAM_EVENT_TYPE_ROUTE`.

- [ ] **Step 4: Run**

Run: `go test ./internal/domain/chat/... ./internal/domain/runs/... ./internal/types/...`
Expected: PASS, including the existing `chat_resume_test.go` and `stream_panic_test.go`.

- [ ] **Step 5: Commit**

```bash
git add internal/types/chat_session.go internal/domain/chat/common/ internal/domain/chat/handler/ internal/domain/runs/tracker.go internal/domain/runs/tracker_test.go
git commit -m "feat(chat): stream carries stop_index and ROUTE; a city's error no longer ends the stream"
```

### Task 7: Trip cities persisted (migration 0101)

**Files:**
- Create: `pkg/db/migrations/0101_trip_cities.up.sql`
- Modify: `internal/domain/trip/repository.go` (type, insert, load)
- Modify: `internal/domain/trip/mappers.go` (`tripToProto`, `tripFromProto`)
- Test: `internal/domain/trip/multicity_integration_test.go`, `internal/domain/trip/trip_test.go`

**Interfaces:**
- Produces: `type TripCity struct { CityName string; CityID *uuid.UUID; SessionID *uuid.UUID; Nights int32; OrderIndex int32 }`, `Trip.Cities []TripCity`. SaveTrip deletes and rewrites `trip_cities` on each save, the same way it handles days and legs.

- [ ] **Step 1: Write the failing tests**

In `trip_test.go` (a unit test of the mappers):

```go
func TestTripCitiesRoundTripThroughProto(t *testing.T) {
	sid := uuid.New()
	in := &Trip{ID: uuid.New(), UserID: uuid.New(), CityName: "Lisbon", Title: "Lisbon + Porto",
		Cities: []TripCity{{CityName: "Lisbon", SessionID: &sid, Nights: 3, OrderIndex: 0}, {CityName: "Porto", Nights: 2, OrderIndex: 1}}}
	p := tripToProto(in)
	if len(p.GetCities()) != 2 || p.GetCities()[0].GetSessionId() != sid.String() || p.GetCities()[1].GetNights() != 2 {
		t.Fatalf("proto cities = %+v", p.GetCities())
	}
	back, err := tripFromProto(p, in.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Cities) != 2 || back.Cities[0].SessionID == nil || *back.Cities[0].SessionID != sid {
		t.Fatalf("round trip lost cities: %+v", back.Cities)
	}
}
```

In `multicity_integration_test.go`, extend `TestRepository_MultiCityTripRoundTrip`: set `in.Cities` to two entries (one with a `SessionID`). After `SaveTrip` + `GetTrip`, assert `len(got.Cities) == 2`, the order is preserved and the session id is kept. Then save again with a single city and assert that exactly 1 remains, which proves the rows are replaced rather than appended.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/domain/trip/ -run TripCitiesRoundTrip -v`
Expected: FAIL, `unknown field Cities`.

- [ ] **Step 3: Implement**

`pkg/db/migrations/0101_trip_cities.up.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
-- The cities of a multi-city trip, in visiting order, each linked to the chat
-- session its places were generated in. trip_days already say which city a
-- day is spent in; this is the list a UI walks to show "Lisbon · 3n → Porto · 2n"
-- and to reopen one city's hotels, restaurants and activities.
CREATE TABLE IF NOT EXISTS trip_cities (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trip_id     UUID NOT NULL REFERENCES trips(id) ON DELETE CASCADE,
    order_index INT  NOT NULL,
    city_name   TEXT NOT NULL,
    city_id     UUID NULL REFERENCES cities(id) ON DELETE SET NULL,
    session_id  UUID NULL,
    nights      INT  NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (trip_id, order_index)
);
CREATE INDEX IF NOT EXISTS idx_trip_cities_trip ON trip_cities (trip_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS trip_cities;
-- +goose StatementEnd
```

Check that `cities(id)` is the real table and column name by grepping an earlier migration for `REFERENCES cities`. Check too that `trip_days.city_id` references the same table; if that column has no FK, drop the FK here as well to match.

`repository.go`:
- Add `TripCity` next to `TripLeg`, and add `Cities []TripCity` to `Trip`, after `Legs`.
- In `SaveTrip`, after the legs replace block: `DELETE FROM trip_cities WHERE trip_id = $1`, then `insertCities(ctx, tx, t)`.
- `insertCities`, written like `insertLegs`:

```go
// insertCities writes a trip's cities in one batched round trip.
func insertCities(ctx context.Context, tx pgx.Tx, t *Trip) error {
	if len(t.Cities) == 0 {
		return nil
	}
	b := &pgx.Batch{}
	for i := range t.Cities {
		c := &t.Cities[i]
		b.Queue(`
			INSERT INTO trip_cities (trip_id, order_index, city_name, city_id, session_id, nights)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			t.ID, c.OrderIndex, c.CityName, c.CityID, c.SessionID, c.Nights)
	}
	br := tx.SendBatch(ctx, b)
	for range t.Cities {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return fmt.Errorf("insert city: %w", err)
		}
	}
	return br.Close()
}
```

- `loadCities`, called from `loadDays` right after `loadLegs`:

```go
// loadCities populates t.Cities in visiting order.
func (r *repository) loadCities(ctx context.Context, t *Trip) error {
	rows, err := r.db.Query(ctx, `
		SELECT order_index, city_name, city_id, session_id, nights
		FROM trip_cities WHERE trip_id = $1 ORDER BY order_index`, t.ID)
	if err != nil {
		return fmt.Errorf("load cities: %w", err)
	}
	defer rows.Close()
	t.Cities = nil
	for rows.Next() {
		var c TripCity
		if err := rows.Scan(&c.OrderIndex, &c.CityName, &c.CityID, &c.SessionID, &c.Nights); err != nil {
			return fmt.Errorf("scan city: %w", err)
		}
		t.Cities = append(t.Cities, c)
	}
	return rows.Err()
}
```

`mappers.go`: in `tripToProto`, after the legs loop:

```go
	for _, c := range t.Cities {
		pc := &tripv1.TripCity{CityName: c.CityName, Nights: c.Nights, OrderIndex: c.OrderIndex}
		if c.CityID != nil {
			pc.CityId = c.CityID.String()
		}
		if c.SessionID != nil {
			pc.SessionId = c.SessionID.String()
		}
		p.Cities = append(p.Cities, pc)
	}
```

In `tripFromProto`, after the legs loop:

```go
	for _, pc := range p.GetCities() {
		c := TripCity{CityName: pc.GetCityName(), Nights: pc.GetNights(), OrderIndex: pc.GetOrderIndex()}
		if id, err := uuid.Parse(pc.GetCityId()); err == nil {
			c.CityID = &id
		}
		if id, err := uuid.Parse(pc.GetSessionId()); err == nil {
			c.SessionID = &id
		}
		t.Cities = append(t.Cities, c)
	}
```

- [ ] **Step 4: Run**

Run: `scripts/check-migrations.sh && go test ./internal/domain/trip/ && go test -tags integration ./internal/domain/trip/ -run MultiCity`
Expected: PASS. The integration run needs the test DB that `testsupport.MustPool` points at; see the repo's `make test-integration` if it exists.

- [ ] **Step 5: Commit**

```bash
git add pkg/db/migrations/0101_trip_cities.up.sql internal/domain/trip/repository.go internal/domain/trip/mappers.go \
  internal/domain/trip/trip_test.go internal/domain/trip/multicity_integration_test.go
git commit -m "feat(trip): persist a multi-city trip's cities and their sessions (0101)"
```

### Task 8: Build the route from what the traveller asked

**Files:**
- Create: `internal/domain/chat/service/chat_multicity_route.go`
- Test: `internal/domain/chat/service/chat_multicity_route_test.go`
- Modify: `internal/domain/chat/service/chat_service.go` (field + setter)
- Modify: `cmd/api/dependencies.go` (wire it)

**Interfaces:**
- Consumes: `multicity.Plan`, `multicity.PlanFixed`, `multicity.FixedStop` (Tasks 3–4); `TripCities` (Task 5); `common.TripStopRequest` (Task 6); `city.ResolveQuery`, `city.Resolved` (`internal/domain/city/city_resolver.go`).
- Produces:
  ```go
  type CityResolver interface { Resolve(ctx context.Context, q city.ResolveQuery) (*city.Resolved, error) }
  func (l *ServiceImpl) SetCityResolver(r CityResolver)
  type multiCityStop struct { Index int; CityName string; CityID uuid.UUID; Lat, Lon float64; Days []int; Nights int; SessionID uuid.UUID }
  type multiCityRoute struct { Stops []multiCityStop; Route multicity.Route; Dropped []locitypes.StreamDroppedStop; Message string }
  type routeRequest struct { Cities []ExtractedCity; Ordered, SuggestOrder bool; TotalDays int; DaysStated bool; Origin *locitypes.UserLocation; Message string }
  func buildMultiCityRoute(ctx context.Context, r CityResolver, req routeRequest) (*multiCityRoute, error) // nil route, nil error = fewer than 2 real cities
  var ErrNoCitiesResolved = errors.New("none of these cities could be found")
  ```

- [ ] **Step 1: Write the failing test** (`chat_multicity_route_test.go`)

```go
package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/city"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

type fakeResolver map[string][2]float64

func (f fakeResolver) Resolve(_ context.Context, q city.ResolveQuery) (*city.Resolved, error) {
	ll, ok := f[strings.ToLower(q.Name)]
	if !ok {
		return nil, city.ErrCityUnresolvable
	}
	return &city.Resolved{City: locitypes.CityDetail{Name: q.Name}, Lat: ll[0], Lon: ll[1]}, nil
}

var iberia = fakeResolver{
	"lisbon": {38.72, -9.14}, "porto": {41.15, -8.61}, "seville": {37.39, -5.98},
	"coimbra": {40.21, -8.43}, "rome": {41.90, 12.50}, "madrid": {40.42, -3.70},
}

func names(r *multiCityRoute) []string {
	out := []string{}
	for _, s := range r.Stops {
		out = append(out, s.CityName)
	}
	return out
}

func TestBuildRoute_OrderedWithDaysKeepsTravellerPlan(t *testing.T) {
	r, err := buildMultiCityRoute(context.Background(), iberia, routeRequest{
		Cities:  []ExtractedCity{{Name: "Porto", Days: 2}, {Name: "Lisbon", Days: 3}},
		Ordered: true, TotalDays: 5, DaysStated: true, Message: "trip",
	})
	if err != nil || r == nil {
		t.Fatalf("route = %v, err = %v", r, err)
	}
	if got := names(r); got[0] != "Porto" || got[1] != "Lisbon" {
		t.Fatalf("order changed: %v", got)
	}
	if len(r.Stops[0].Days) != 2 || len(r.Stops[1].Days) != 3 || r.Stops[1].Days[0] != 3 {
		t.Fatalf("days = %v / %v", r.Stops[0].Days, r.Stops[1].Days)
	}
}

// Review Focus #1: no duration stated must not starve the route of days.
func TestBuildRoute_NoDurationWidensWindow(t *testing.T) {
	r, err := buildMultiCityRoute(context.Background(), iberia, routeRequest{
		Cities:    []ExtractedCity{{Name: "Lisbon"}, {Name: "Porto"}, {Name: "Seville"}},
		TotalDays: 2, DaysStated: false, Message: "trip",
	})
	if err != nil || r == nil {
		t.Fatalf("route = %v, err = %v", r, err)
	}
	if len(r.Stops) != 3 {
		t.Fatalf("expected all three cities kept, got %v (dropped %+v)", names(r), r.Dropped)
	}
	if r.Route.Days[len(r.Route.Days)-1].DayNumber < 6 {
		t.Fatalf("window should be at least 2 days per city")
	}
}

func TestBuildRoute_SuggestOrderReorders(t *testing.T) {
	r, _ := buildMultiCityRoute(context.Background(), iberia, routeRequest{
		Cities:       []ExtractedCity{{Name: "Lisbon", Days: 2}, {Name: "Seville", Days: 2}, {Name: "Porto", Days: 2}},
		Ordered:      true, SuggestOrder: true, TotalDays: 6, DaysStated: true, Message: "trip",
	})
	if got := names(r); got[0] != "Lisbon" || got[1] != "Porto" {
		t.Fatalf("expected nearest-neighbour Lisbon → Porto → Seville, got %v", got)
	}
}

// Review Focus #5.
func TestBuildRoute_UnresolvableIsDroppedNotFatal(t *testing.T) {
	r, err := buildMultiCityRoute(context.Background(), iberia, routeRequest{
		Cities: []ExtractedCity{{Name: "Lisbon", Days: 2}, {Name: "Atlantis", Days: 2}, {Name: "Porto", Days: 2}},
		Ordered: true, TotalDays: 6, DaysStated: true, Message: "trip",
	})
	if err != nil || r == nil || len(r.Stops) != 2 {
		t.Fatalf("expected Lisbon + Porto, got %v err %v", r, err)
	}
	if len(r.Dropped) != 1 || r.Dropped[0].CityName != "Atlantis" || r.Dropped[0].Reason == "" {
		t.Fatalf("dropped = %+v", r.Dropped)
	}
}

func TestBuildRoute_OneRealCityFallsBack(t *testing.T) {
	r, err := buildMultiCityRoute(context.Background(), iberia, routeRequest{
		Cities: []ExtractedCity{{Name: "Lisbon"}, {Name: "Atlantis"}}, Message: "trip", TotalDays: 3,
	})
	if err != nil || r != nil {
		t.Fatalf("expected nil route (single-city fallback), got %v err %v", r, err)
	}
}

func TestBuildRoute_NoneResolve(t *testing.T) {
	_, err := buildMultiCityRoute(context.Background(), iberia, routeRequest{
		Cities: []ExtractedCity{{Name: "Atlantis"}, {Name: "Lemuria"}}, Message: "trip", TotalDays: 3,
	})
	if !errors.Is(err, ErrNoCitiesResolved) {
		t.Fatalf("err = %v, want ErrNoCitiesResolved", err)
	}
}

func TestBuildRoute_CapsAtFive(t *testing.T) {
	six := []ExtractedCity{{Name: "Lisbon"}, {Name: "Porto"}, {Name: "Seville"}, {Name: "Coimbra"}, {Name: "Madrid"}, {Name: "Rome"}}
	r, err := buildMultiCityRoute(context.Background(), iberia, routeRequest{Cities: six, TotalDays: 14, DaysStated: true, Message: "trip"})
	if err != nil || r == nil || len(r.Stops) > 5 {
		t.Fatalf("expected at most 5 stops, got %v err %v", r, err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/domain/chat/service/ -run BuildRoute -v`
Expected: FAIL, `undefined: buildMultiCityRoute`.

- [ ] **Step 3: Implement** (`chat_multicity_route.go`)

```go
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/city"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/multicity"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// maxTripCities is the most cities one trip plans. Each costs a full
// generation, one after another.
const maxTripCities = 5

// minDaysPerCity is the least a city gets when the traveller did not say how
// long the trip is. The default horizon is a short sample; three cities in it
// would leave the planner dropping two of them.
const minDaysPerCity = 2

var ErrNoCitiesResolved = errors.New("none of these cities could be found")

// CityResolver is the geocoder the compare service already uses.
type CityResolver interface {
	Resolve(ctx context.Context, q city.ResolveQuery) (*city.Resolved, error)
}

type multiCityStop struct {
	Index     int
	CityName  string
	CityID    uuid.UUID
	Lat, Lon  float64
	Days      []int // trip-wide day numbers
	Nights    int
	SessionID uuid.UUID
}

type multiCityRoute struct {
	Stops   []multiCityStop
	Route   multicity.Route
	Dropped []locitypes.StreamDroppedStop
	// Message is the request with the cities taken out: what each city's
	// generation is asked, so its cache key matches a single-city request.
	Message string
}

type routeRequest struct {
	Cities       []ExtractedCity
	Ordered      bool
	SuggestOrder bool
	TotalDays    int
	DaysStated   bool
	Origin       *locitypes.UserLocation
	Message      string
}

// buildMultiCityRoute resolves the cities and lays out the trip. It returns a
// nil route and no error when fewer than two real cities remain: that request
// is an ordinary single-city one and takes today's path.
func buildMultiCityRoute(ctx context.Context, r CityResolver, req routeRequest) (*multiCityRoute, error) {
	out := &multiCityRoute{Message: req.Message}

	var resolved []multicity.City
	nights := map[string]int{}
	for _, c := range req.Cities {
		if len(resolved) == maxTripCities {
			out.Dropped = append(out.Dropped, locitypes.StreamDroppedStop{CityName: c.Name, Reason: fmt.Sprintf("a trip plans at most %d cities", maxTripCities)})
			continue
		}
		q := city.ResolveQuery{Name: c.Name}
		if req.Origin != nil {
			q.NearLat, q.NearLon = req.Origin.UserLat, req.Origin.UserLon
		}
		got, err := r.Resolve(ctx, q)
		if err != nil || got == nil {
			out.Dropped = append(out.Dropped, locitypes.StreamDroppedStop{CityName: c.Name, Reason: "we couldn't find this city"})
			continue
		}
		name := got.City.Name
		if name == "" {
			name = c.Name
		}
		resolved = append(resolved, multicity.City{ID: got.City.ID.String(), Name: name, Lat: got.Lat, Lon: got.Lon})
		nights[name] = c.Days
	}

	switch len(resolved) {
	case 0:
		return nil, ErrNoCitiesResolved
	case 1:
		return nil, nil
	}

	allNights := true
	for _, c := range resolved {
		if nights[c.Name] <= 0 {
			allNights = false
		}
	}

	in := multicity.Input{MultiModal: true, MaxCities: maxTripCities}
	var route multicity.Route
	if req.Ordered && allNights && !req.SuggestOrder {
		stops := make([]multicity.FixedStop, len(resolved))
		for i, c := range resolved {
			stops[i] = multicity.FixedStop{City: c, Nights: nights[c.Name]}
		}
		route = multicity.PlanFixed(in, stops)
	} else {
		days := req.TotalDays
		if allNights {
			days = 0
			for _, c := range resolved {
				days += nights[c.Name]
			}
		}
		if floor := minDaysPerCity * len(resolved); !req.DaysStated && days < floor {
			days = floor
		}
		start := time.Unix(0, 0).UTC()
		in.Start, in.End = start, start.Add(time.Duration(days)*24*time.Hour)
		in.Candidates = resolved
		route = multicity.Plan(in)
		for _, d := range route.Dropped {
			out.Dropped = append(out.Dropped, locitypes.StreamDroppedStop{CityName: d.CityName, Reason: d.Reason})
		}
	}

	if len(route.Cities) < 2 {
		// The planner kept one city: still a single-city trip, and the
		// dropped reasons are logged by the caller.
		return nil, nil
	}

	out.Route = route
	for i, c := range route.Cities {
		s := multiCityStop{Index: i, CityName: c.Name, Lat: c.Lat, Lon: c.Lon}
		if id, err := uuid.Parse(c.ID); err == nil {
			s.CityID = id
		}
		for _, d := range route.Days {
			if d.CityName == c.Name {
				s.Days = append(s.Days, d.DayNumber)
			}
		}
		s.Nights = len(s.Days)
		out.Stops = append(out.Stops, s)
	}
	return out, nil
}
```

Before writing this, check `locitypes.CityDetail`'s real field names (`ID`, `Name`) with `grep -n "type CityDetail struct" -A10 internal/types/*.go`. Also check `city.ErrCityUnresolvable` exists; it is declared at `city_resolver.go:90`.

`chat_service.go`: add a field `cityResolver CityResolver` on `ServiceImpl`, next to `cityRepo`, and

```go
// SetCityResolver gives the service the geocoder multi-city trips resolve
// their cities with. Without it every multi-city request degrades to its
// first city.
func (l *ServiceImpl) SetCityResolver(r CityResolver) { l.cityResolver = r }
```

`cmd/api/dependencies.go`: right after `chatSvc.SetPreferenceVectors(d.PreferenceVectors)`, add `chatSvc.SetCityResolver(d.CityResolver)`. `d.CityResolver` is built at line 378, before this point, and `*city.ResolverImpl` satisfies the interface.

- [ ] **Step 4: Run**

Run: `go test ./internal/domain/chat/service/ -run BuildRoute -v && go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/chat/service/chat_multicity_route.go internal/domain/chat/service/chat_multicity_route_test.go \
  internal/domain/chat/service/chat_service.go cmd/api/dependencies.go
git commit -m "feat(chat): resolve and lay out a multi-city route from the request"
```

### Task 9: The orchestrator

**Files:**
- Create: `internal/domain/chat/service/chat_multicity.go`
- Test: `internal/domain/chat/service/chat_multicity_test.go`
- Modify: `internal/domain/chat/service/chat_stream_session.go:659-725` (dispatch; the body becomes `runSingleCity`)
- Modify: `internal/domain/chat/service/chat_process_stream.go` (`prepareChatContext` presets; `persistResults` skips the trip save for child runs)

**Interfaces:**
- Consumes: everything from Tasks 5–8; `buildTripFromCityResponse` (`chat_trip.go:91`); `l.tripRepo.SaveTrip`; `l.sendEvent`; `l.sendCompletionEvent`.
- Produces:
  ```go
  func (l *ServiceImpl) runSingleCity(cc common.ChatContext) (*locitypes.AiCityResponse, error)
  func (l *ServiceImpl) planMultiCity(cc *common.ChatContext) (*multiCityRoute, error)
  func (l *ServiceImpl) processMultiCity(cc common.ChatContext, r *multiCityRoute) error
  func forwardStopEvent(ev locitypes.StreamEvent, index int) (locitypes.StreamEvent, bool) // bool=false: drop
  func mergeStopTrips(userID uuid.UUID, r *multiCityRoute, perStop []*trip.Trip) *trip.Trip
  func routeData(r *multiCityRoute, tripID string) locitypes.StreamRouteData
  // seam for tests:
  // ServiceImpl gains the field  runCityFn func(common.ChatContext) (*locitypes.AiCityResponse, error)  and the method  runCity(cc)  (nil field → l.runSingleCity)
  ```

- [ ] **Step 1: Write the failing tests** (`chat_multicity_test.go`)

```go
package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/multicity"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func twoCityRoute() *multiCityRoute {
	route := multicity.PlanFixed(multicity.Input{MultiModal: true}, []multicity.FixedStop{
		{City: multicity.City{Name: "Lisbon", Lat: 38.72, Lon: -9.14}, Nights: 2},
		{City: multicity.City{Name: "Porto", Lat: 41.15, Lon: -8.61}, Nights: 1},
	})
	return &multiCityRoute{
		Route: route, Message: "itinerary",
		Stops: []multiCityStop{
			{Index: 0, CityName: "Lisbon", Days: []int{1, 2}, Nights: 2, SessionID: uuid.New()},
			{Index: 1, CityName: "Porto", Days: []int{3}, Nights: 1, SessionID: uuid.New()},
		},
	}
}

func TestForwardStopEvent(t *testing.T) {
	ev, keep := forwardStopEvent(locitypes.StreamEvent{Type: locitypes.EventTypeItinerary}, 1)
	if !keep || ev.StopIndex == nil || *ev.StopIndex != 1 {
		t.Fatalf("itinerary should be tagged with stop 1: %+v", ev)
	}
	if _, keep := forwardStopEvent(locitypes.StreamEvent{Type: locitypes.EventTypeComplete}, 0); keep {
		t.Fatal("a city's COMPLETE must not reach the client; the run completes once")
	}
	ev, keep = forwardStopEvent(locitypes.StreamEvent{Type: locitypes.EventTypeStart, Data: locitypes.StreamStartData{SessionID: "s1"}}, 1)
	if !keep || ev.Type != locitypes.EventTypeProgress {
		t.Fatalf("a city's START becomes progress (one START names the run): %+v", ev)
	}
	ev, _ = forwardStopEvent(locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: "x"}, 0)
	if ev.IsTerminal() {
		t.Fatal("a city's ERROR must not be terminal")
	}
}

func TestMergeStopTrips(t *testing.T) {
	r := twoCityRoute()
	lisbon := &trip.Trip{Days: []trip.TripDay{{DayNumber: 1, Stops: []trip.TripStop{{Name: "Belém"}}}, {DayNumber: 2}}}
	porto := &trip.Trip{Days: []trip.TripDay{{DayNumber: 1, Stops: []trip.TripStop{{Name: "Ribeira"}}}}}
	user := uuid.New()

	got := mergeStopTrips(user, r, []*trip.Trip{lisbon, porto})

	if got.Title != "Lisbon + Porto" || got.CityName != "Lisbon" || got.UserID != user {
		t.Fatalf("header = %q / %q", got.Title, got.CityName)
	}
	if len(got.Days) != 3 {
		t.Fatalf("expected 3 days, got %d", len(got.Days))
	}
	if d := got.Days[2]; d.DayNumber != 3 || d.CityName != "Porto" || !d.TravelDay || d.Stops[0].Name != "Ribeira" {
		t.Fatalf("day 3 = %+v", d)
	}
	if got.Days[1].TravelDay {
		t.Fatal("day 2 is not a travel day")
	}
	if len(got.Legs) != 1 || got.Legs[0].AfterDay != 2 || got.Legs[0].Mode != "train" {
		t.Fatalf("legs = %+v", got.Legs)
	}
	if len(got.Cities) != 2 || got.Cities[1].SessionID == nil || *got.Cities[1].SessionID != r.Stops[1].SessionID {
		t.Fatalf("cities = %+v", got.Cities)
	}
	if got.SourceSessionID == nil || *got.SourceSessionID != r.Stops[0].SessionID.String() {
		t.Fatal("the parent trip's source session is the first city's")
	}
}

// Review Focus #2: a city that fails still gets its days, and the others keep theirs.
func TestMergeStopTrips_FailedCityKeepsItsDays(t *testing.T) {
	r := twoCityRoute()
	got := mergeStopTrips(uuid.New(), r, []*trip.Trip{nil, {Days: []trip.TripDay{{DayNumber: 1}}}})
	if len(got.Days) != 3 || got.Days[0].CityName != "Lisbon" || len(got.Days[0].Stops) != 0 {
		t.Fatalf("days = %+v", got.Days)
	}
}

func TestProcessMultiCity_EventOrderAndFailureIsolation(t *testing.T) {
	events := make(chan locitypes.StreamEvent, 200)
	l := newTestService(t) // existing helper in test_helpers.go; check its name
	l.runCityFn = func(cc common.ChatContext) (*locitypes.AiCityResponse, error) {
		if cc.CityName == "Porto" {
			cc.EventCh <- locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: "porto failed"}
			return nil, errors.New("porto failed")
		}
		cc.EventCh <- locitypes.StreamEvent{Type: locitypes.EventTypeStart, Data: locitypes.StreamStartData{SessionID: cc.PresetSessionID.String()}}
		cc.EventCh <- locitypes.StreamEvent{Type: locitypes.EventTypeItinerary, Data: locitypes.AiCityResponse{}}
		cc.EventCh <- locitypes.StreamEvent{Type: locitypes.EventTypeComplete}
		if !cc.StopRun || !cc.SuppressTripSave || cc.PresetTripDays != 2 {
			t.Errorf("child context not preset: %+v", cc)
		}
		return &locitypes.AiCityResponse{}, nil
	}
	err := l.processMultiCity(common.ChatContext{Ctx: context.Background(), UserID: uuid.New(), EventCh: events}, twoCityRoute())
	close(events)
	if err != nil {
		t.Fatalf("one city failing must not fail the run: %v", err)
	}

	var types []string
	var stopsOnError []int
	for ev := range events {
		types = append(types, ev.Type)
		if ev.Type == locitypes.EventTypeError && ev.StopIndex != nil {
			stopsOnError = append(stopsOnError, *ev.StopIndex)
		}
	}
	if types[0] != locitypes.EventTypeStart || types[1] != locitypes.EventTypeRoute {
		t.Fatalf("stream must open START, ROUTE; got %v", types)
	}
	if types[len(types)-1] != locitypes.EventTypeComplete {
		t.Fatalf("stream must end COMPLETE; got %v", types)
	}
	starts, completes := 0, 0
	for _, ty := range types {
		switch ty {
		case locitypes.EventTypeStart:
			starts++
		case locitypes.EventTypeComplete:
			completes++
		}
	}
	if starts != 1 || completes != 1 {
		t.Fatalf("exactly one START and one COMPLETE, got %d/%d in %v", starts, completes, types)
	}
	if len(stopsOnError) != 1 || stopsOnError[0] != 1 {
		t.Fatalf("Porto's error must be tagged stop 1: %v", stopsOnError)
	}
}

func TestProcessMultiCity_AllFail(t *testing.T) {
	events := make(chan locitypes.StreamEvent, 50)
	l := newTestService(t)
	l.runCityFn = func(common.ChatContext) (*locitypes.AiCityResponse, error) { return nil, errors.New("down") }
	err := l.processMultiCity(common.ChatContext{Ctx: context.Background(), EventCh: events}, twoCityRoute())
	close(events)
	if err == nil {
		t.Fatal("every city failing is a failed run")
	}
	var last locitypes.StreamEvent
	for ev := range events {
		last = ev
	}
	if !last.IsTerminal() || last.Type != locitypes.EventTypeError {
		t.Fatalf("expected a terminal untagged ERROR last, got %+v", last)
	}
}
```

`newTestService`: use whatever `test_helpers.go` already provides for a `*ServiceImpl` with a nil trip repo and a discard logger. If nothing fits, add `func newTestService(t *testing.T) *ServiceImpl { return &ServiceImpl{logger: slog.New(slog.NewTextHandler(io.Discard, nil))} }` there. With a nil `tripRepo`, the orchestrator must skip saving.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/domain/chat/service/ -run 'ForwardStopEvent|MergeStopTrips|ProcessMultiCity' -v`
Expected: FAIL, undefined symbols.

- [ ] **Step 3: Implement**

`chat_process_stream.go`, in `prepareChatContext`:
- Replace step 0 so it reads:

```go
	if cc.PresetTripDays > 0 {
		cc.TripDays, cc.TripDaysSource = cc.PresetTripDays, "multicity"
	} else {
		span := tripspan.Parse(cc.Message)
		cc.TripDays, cc.TripDaysSource = span.Days, string(span.Source)
		observability.RecordTripDurationParse(cc.TripDaysSource, cc.TripDays)
		if !span.Parsed() {
			l.logger.InfoContext(ctx, "no trip duration in the request; assuming a short sample",
				slog.String("message", cc.Message), slog.Int("assumed_days", cc.TripDays))
		}
	}
```

- Wrap step 1 as `if !cc.StopRun { …existing extraction… }`. A child run's `CityName` and `Message` were set by the orchestrator.
- In step 4, change `cc.SessionID = uuid.New()` to:

```go
		cc.SessionID = cc.PresetSessionID
		if cc.SessionID == uuid.Nil {
			cc.SessionID = uuid.New()
		}
```

In `persistResults` (line ~707), change the guard to `if l.tripRepo != nil && !cc.SuppressTripSave && (…domain check…)`.

`chat_stream_session.go`: rename the existing `ProcessUnifiedChatMessageStream` body to `runSingleCity(cc common.ChatContext) (*locitypes.AiCityResponse, error)`, returning `data` on success and `nil, err` everywhere it returned `err`. Inside it, directly after the tracing span, apply the per-city budget:

```go
	// One city's budget, whatever the stream's: a multi-city stream runs
	// several of these one after another.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cc.Ctx = ctx
```

Then the new public entry point:

```go
// ProcessUnifiedChatMessageStream answers one chat turn. A turn naming two or
// more cities (or carrying stops from the builder) is a multi-city trip,
// answered by running the single-city pipeline once per city; anything else
// is exactly the single-city turn it always was.
func (l *ServiceImpl) ProcessUnifiedChatMessageStream(cc common.ChatContext) error {
	if !cc.StopRun {
		route, err := l.planMultiCity(&cc)
		if err != nil {
			l.sendEvent(cc.Ctx, cc.EventCh, locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: err.Error()}, 3)
			return err
		}
		if route != nil {
			return l.processMultiCity(cc, route)
		}
	}
	_, err := l.runCity(cc)
	return err
}
```

`l.runCity(cc)` is a method over a test seam field `runCityFn` (add the field to `ServiceImpl`):

```go
func (l *ServiceImpl) runCity(cc common.ChatContext) (*locitypes.AiCityResponse, error) {
	if l.runCityFn != nil {
		return l.runCityFn(cc)
	}
	return l.runSingleCity(cc)
}
```

`chat_multicity.go`:

```go
package service

import (
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/tripspan"
)

var errNoCityPlanned = errors.New("we couldn't plan any of these cities — try again in a moment")

// planMultiCity decides whether this turn is a multi-city trip. It returns a
// nil route for a single-city turn. The extractor call it makes is cached on
// the message, so the single-city path that follows pays nothing for it.
func (l *ServiceImpl) planMultiCity(cc *common.ChatContext) (*multiCityRoute, error) {
	if l.cityResolver == nil {
		return nil, nil
	}
	span := tripspan.Parse(cc.Message)
	req := routeRequest{
		TotalDays:    span.Days,
		DaysStated:   span.Parsed(),
		Origin:       cc.UserLocation,
		SuggestOrder: cc.SuggestOrder,
		Message:      cc.Message,
	}
	if len(cc.Stops) >= 2 {
		req.Ordered = true
		for _, s := range cc.Stops {
			req.Cities = append(req.Cities, ExtractedCity{Name: s.CityName, Days: s.Nights})
		}
	} else {
		tc, err := l.extractTripCitiesCached(cc.Ctx, cc.Message)
		if err != nil || len(tc.Cities) < 2 {
			// Not knowing is not a reason to fail: the single-city path
			// extracts again (from cache) and reports its own errors.
			return nil, nil
		}
		req.Cities, req.Ordered, req.Message = tc.Cities, tc.Ordered, tc.Message
	}
	route, err := buildMultiCityRoute(cc.Ctx, l.cityResolver, req)
	if err != nil {
		return nil, err
	}
	if route == nil && len(cc.Stops) >= 2 {
		// The builder asked for several cities and only one survived: plan it
		// as that city, rather than re-extracting from free text.
		for _, s := range cc.Stops {
			if s.CityName != "" {
				cc.CityName = s.CityName
				break
			}
		}
	}
	return route, nil
}

// processMultiCity runs the single-city pipeline once per city, in order, on
// one stream: START and ROUTE first, then each city's events tagged with its
// index, then one parent trip and one COMPLETE.
func (l *ServiceImpl) processMultiCity(cc common.ChatContext, r *multiCityRoute) error {
	ctx := cc.Ctx
	for i := range r.Stops {
		if r.Stops[i].SessionID == uuid.Nil {
			r.Stops[i].SessionID = uuid.New()
		}
	}
	first := r.Stops[0]
	domain := (&locitypes.DomainDetector{}).DetectDomain(ctx, r.Message)

	// The first city's session names the run: the resume buffer, the run
	// tracker and the client's URL all key on the START's session id.
	l.sendEvent(ctx, cc.EventCh, locitypes.StreamEvent{
		Type: locitypes.EventTypeStart,
		Data: locitypes.StreamStartData{SessionID: first.SessionID.String(), Domain: string(domain), City: first.CityName},
	}, 3)
	l.sendEvent(ctx, cc.EventCh, locitypes.StreamEvent{Type: locitypes.EventTypeRoute, Data: routeData(r, "")}, 3)

	perStop := make([]*trip.Trip, len(r.Stops))
	succeeded := 0
	for i, s := range r.Stops {
		child := cc
		child.CityName = s.CityName
		child.Message = r.Message
		child.StopRun = true
		child.PresetSessionID = s.SessionID
		child.PresetTripDays = len(s.Days)
		child.SuppressTripSave = true
		child.RequestedSessionID = uuid.Nil
		child.TripID = uuid.Nil
		child.Stops = nil

		stopCh := make(chan locitypes.StreamEvent, 100)
		done := make(chan struct{})
		go func(index int) {
			defer close(done)
			for ev := range stopCh {
				if out, keep := forwardStopEvent(ev, index); keep {
					l.sendEvent(ctx, cc.EventCh, out, 3)
				}
			}
		}(i)
		child.EventCh = stopCh

		started := time.Now()
		data, err := l.runCity(child)
		close(stopCh)
		<-done

		if err != nil {
			l.logger.WarnContext(ctx, "multi-city: a city failed; continuing with the rest",
				slog.Int("stop", i), slog.String("city", s.CityName), slog.Any("error", err))
			continue
		}
		succeeded++
		l.logger.InfoContext(ctx, "multi-city: city generated",
			slog.Int("stop", i), slog.String("city", s.CityName), slog.Duration("took", time.Since(started)))
		child.SessionID = s.SessionID
		perStop[i] = buildTripFromCityResponse(&child, data, "")
	}

	if succeeded == 0 {
		l.sendEvent(ctx, cc.EventCh, locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: errNoCityPlanned.Error()}, 3)
		return errNoCityPlanned
	}

	done := cc
	done.SessionID = first.SessionID
	done.CityName = first.CityName
	done.Domain = domain
	if l.tripRepo != nil {
		saved, err := l.tripRepo.SaveTrip(ctx, mergeStopTrips(cc.UserID, r, perStop), 0)
		if err != nil {
			l.logger.WarnContext(ctx, "multi-city: parent trip not saved", slog.Any("error", err))
		} else {
			done.TripID = saved.ID
			l.sendEvent(ctx, cc.EventCh, locitypes.StreamEvent{Type: locitypes.EventTypeRoute, Data: routeData(r, saved.ID.String())}, 3)
		}
	}
	l.sendCompletionEvent(&done)
	return nil
}

// forwardStopEvent adapts one city's event for the multi-city stream. The
// city's own START becomes progress (the run already has one), its COMPLETE is
// dropped (the run completes once), and everything else is tagged with the
// city's index — which also makes its ERROR non-terminal.
func forwardStopEvent(ev locitypes.StreamEvent, index int) (locitypes.StreamEvent, bool) {
	idx := index
	ev.StopIndex = &idx
	switch ev.Type {
	case locitypes.EventTypeComplete:
		return ev, false
	case locitypes.EventTypeStart:
		ev.Type = locitypes.EventTypeProgress
		ev.Message = "city_started"
	}
	return ev, true
}

// routeData is the ROUTE payload for this route.
func routeData(r *multiCityRoute, tripID string) locitypes.StreamRouteData {
	out := locitypes.StreamRouteData{
		Outline:         r.Route.Outline,
		Warnings:        r.Route.Warnings,
		Dropped:         r.Dropped,
		TotalTravelMins: r.Route.TotalTravelMins,
		TripID:          tripID,
	}
	for _, s := range r.Stops {
		rs := locitypes.StreamRouteStop{Index: s.Index, CityName: s.CityName, SessionID: s.SessionID.String(), DayNumbers: s.Days}
		if s.CityID != uuid.Nil {
			rs.CityID = s.CityID.String()
		}
		out.Stops = append(out.Stops, rs)
	}
	for _, lg := range r.Route.Legs {
		out.Legs = append(out.Legs, locitypes.StreamRouteLeg{
			AfterDay: lg.AfterDay, FromName: lg.FromName, ToName: lg.ToName,
			FromLat: lg.FromLat, FromLon: lg.FromLon, ToLat: lg.ToLat, ToLon: lg.ToLon,
			DistanceKm: lg.DistanceKm, DurationMins: lg.DurationMins, Mode: lg.Mode,
		})
	}
	return out
}

// mergeStopTrips folds each city's generated days into one trip: days
// renumbered across the route, each tagged with its city, the arrival day in
// every city after the first marked as a travel day, plus the legs and the
// cities linked to their sessions. A city that failed keeps its (empty) days,
// so the trip still shows where the traveller is on those days.
func mergeStopTrips(userID uuid.UUID, r *multiCityRoute, perStop []*trip.Trip) *trip.Trip {
	names := make([]string, len(r.Stops))
	for i, s := range r.Stops {
		names[i] = s.CityName
	}
	source := r.Stops[0].SessionID.String()
	out := &trip.Trip{
		UserID:          userID,
		CityName:        r.Stops[0].CityName,
		Title:           strings.Join(names, " + "),
		SourceSessionID: &source,
	}
	if r.Stops[0].CityID != uuid.Nil {
		id := r.Stops[0].CityID
		out.CityID = &id
	}

	for i, s := range r.Stops {
		var local []trip.TripDay
		if i < len(perStop) && perStop[i] != nil {
			local = perStop[i].Days
		}
		for k, dayNum := range s.Days {
			day := trip.TripDay{DayNumber: int32(dayNum), CityName: s.CityName, TravelDay: k == 0 && i > 0}
			lat, lon := s.Lat, s.Lon
			day.CityLat, day.CityLon = &lat, &lon
			if s.CityID != uuid.Nil {
				id := s.CityID
				day.CityID = &id
			}
			for _, ld := range local {
				if int(ld.DayNumber) == k+1 {
					day.Stops = ld.Stops
				}
			}
			out.Days = append(out.Days, day)
		}
		sid := s.SessionID
		c := trip.TripCity{CityName: s.CityName, SessionID: &sid, Nights: int32(s.Nights), OrderIndex: int32(i)}
		if s.CityID != uuid.Nil {
			id := s.CityID
			c.CityID = &id
		}
		out.Cities = append(out.Cities, c)
	}

	for _, lg := range r.Route.Legs {
		fromLat, fromLon, toLat, toLon := lg.FromLat, lg.FromLon, lg.ToLat, lg.ToLon
		out.Legs = append(out.Legs, trip.TripLeg{
			AfterDay: int32(lg.AfterDay), FromName: lg.FromName, ToName: lg.ToName,
			FromLat: &fromLat, FromLon: &fromLon, ToLat: &toLat, ToLon: &toLon,
			DistanceKm: lg.DistanceKm, DurationMins: int32(lg.DurationMins), Mode: lg.Mode,
		})
	}
	return out
}

```

Run `goimports -w` on the file afterwards.

- [ ] **Step 4: Run**

Run: `go test ./internal/domain/chat/... -v -run 'ForwardStopEvent|MergeStopTrips|ProcessMultiCity' && go test ./internal/domain/chat/... ./internal/domain/runs/...`
Expected: PASS. The existing single-city tests (`chat_stream_worker_test.go`, `generation_*_test.go`, `chat_resume_test.go`) must pass unchanged. That is the "single-city stream unchanged" guarantee.

Add one more test to `chat_multicity_test.go`, for the single-city golden case:

```go
func TestProcessUnified_SingleCityNeverDispatches(t *testing.T) {
	l := newTestService(t)
	l.cityResolver = iberia
	called := 0
	l.runCityFn = func(cc common.ChatContext) (*locitypes.AiCityResponse, error) { called++; return nil, nil }
	// Stub the extractor via the cache: one city in the message.
	l.cache = newTestCache(t) // whatever cachestore fake test_helpers.go offers
	raw, _ := json.Marshal(TripCities{Cities: []ExtractedCity{{Name: "Lisbon"}}, Message: "itinerary"})
	l.cache.Set(tripCitiesCacheKey("3 days in Lisbon"), string(raw), time.Hour)
	_ = l.ProcessUnifiedChatMessageStream(common.ChatContext{Ctx: context.Background(), Message: "3 days in Lisbon", EventCh: make(chan locitypes.StreamEvent, 10)})
	if called != 1 {
		t.Fatalf("single-city turn should run exactly one city, ran %d", called)
	}
}
```

- [ ] **Step 5: Commit**

```bash
git add internal/domain/chat/service/chat_multicity.go internal/domain/chat/service/chat_multicity_test.go \
  internal/domain/chat/service/chat_stream_session.go internal/domain/chat/service/chat_process_stream.go \
  internal/domain/chat/service/chat_service.go internal/domain/chat/service/test_helpers.go
git commit -m "feat(chat): multi-city trips — one stream, one child session per city, one parent trip"
```

### Task 10: Follow-ups and Telegram

**Files:**
- Modify: `internal/domain/chat/service/chat_new_trip.go` (`newTripCity`)
- Test: `internal/domain/chat/service/chat_new_trip_test.go` (exists from api #85; add cases)
- Check: the Telegram bridge formatter (grep `EventTypeItinerary` under `internal/domain/messaging` or `integrations/telegram`)

**Interfaces:**
- Produces: `newTripCity` returns `(city string, ok bool)` as before, and also returns `ok=true` when the follow-up names two or more cities, with `city` = the first.

- [ ] **Step 1: Write the failing test**

```go
// Review Focus #4.
func TestNewTripCity_TwoCitiesIsANewTrip(t *testing.T) {
	l := newTestService(t)
	l.cache = newTestCache(t)
	raw, _ := json.Marshal(TripCities{Cities: []ExtractedCity{{Name: "Porto"}, {Name: "Seville"}}, Message: "add"})
	l.cache.Set(tripCitiesCacheKey("now add Porto and Seville"), string(raw), time.Hour)
	session := &locitypes.ChatSession{CityName: "Lisbon", SessionContext: locitypes.SessionContext{CityName: "Lisbon"}}

	city, ok := l.newTripCity(context.Background(), session, "now add Porto and Seville")
	if !ok || city != "Porto" {
		t.Fatalf("got %q/%v, want Porto/true", city, ok)
	}
}

func TestNewTripCity_SessionCityPlusAnotherIsANewTrip(t *testing.T) {
	l := newTestService(t)
	l.cache = newTestCache(t)
	raw, _ := json.Marshal(TripCities{Cities: []ExtractedCity{{Name: "Lisbon"}, {Name: "Porto"}}, Message: "then"})
	l.cache.Set(tripCitiesCacheKey("Lisbon then Porto"), string(raw), time.Hour)
	session := &locitypes.ChatSession{SessionContext: locitypes.SessionContext{CityName: "Lisbon"}}
	if _, ok := l.newTripCity(context.Background(), session, "Lisbon then Porto"); !ok {
		t.Fatal("naming the session city and another is a multi-city trip, not an edit")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/domain/chat/service/ -run NewTripCity -v`
Expected: FAIL. The second case returns false today (the first city is the session's own).

- [ ] **Step 3: Implement.** In `newTripCity`, replace the `extractCityCached` call with:

```go
	tc, err := l.extractTripCitiesCached(ctx, message)
	if err != nil {
		l.logger.WarnContext(ctx, "could not read a city out of the follow-up; continuing the session",
			slog.Any("error", err))
		return "", false
	}
	// Two or more cities is a multi-city trip, whatever the session is about:
	// ProcessUnifiedChatMessageStream, which this hands off to, plans it.
	if len(tc.Cities) >= 2 {
		return tc.First(), true
	}
	extracted := tc.First()
```

and leave the rest as it is.

Telegram: find the bridge's result formatter (`grep -rn "EventTypeItinerary\|EventTypeComplete" internal --include='*.go' | grep -i telegram`). If it keeps the last itinerary event, it will show only the last city. Change it so that on an `EventTypeRoute` event it sends `rd.Outline` as a message first, and for each itinerary event with a `StopIndex` it prefixes the summary with the city name from the route. Add a table test next to the existing formatter test: a route event plus two tagged itineraries produces three messages, the first being the outline. If the bridge formats only from `COMPLETE`'s navigation link, it needs no change; record that in the PR description.

- [ ] **Step 4: Run**

Run: `go test ./internal/domain/chat/... ./internal/domain/messaging/... 2>/dev/null; go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit and open the server PR**

```bash
git add internal/domain/chat/service/chat_new_trip.go internal/domain/chat/service/chat_new_trip_test.go <telegram files if changed>
git commit -m "feat(chat): a follow-up naming several cities starts a multi-city trip"
git push -u origin feat/multi-city-trips
gh pr create --title "Multi-city trips" --body-file <(printf 'Implements docs/superpowers/specs/2026-09-24-multi-city-trips-design.md.\n\nMigration 0101_trip_cities. Proto v5.26.0.\n')
```

Wait for CI to go green. **Do not merge until Parts C and D are ready**, because all four ship together. The server is backward compatible, so if the user prefers, it can merge first.

**Local stream check (before merge):** run the server locally (`make run` or the repo's documented target), then:

```bash
buf curl --schema ../loci-connect-proto-multicity --protocol connect \
  -H "Authorization: Bearer $LOCI_DEV_TOKEN" \
  -d '{"message":"Lisbon for 2 days then Porto for 1 day"}' \
  http://localhost:8000/loci.chat.ChatService/StreamChat | jq -c '{t: .eventType, stop: .stopIndex, route: (.route.outline // null)}'
```

Expected: `START`, then `ROUTE` with outline "Lisbon (2 days) → Porto (1 day) · ≈…", then events with `stop: 0`, then `stop: 1`, then a `ROUTE` with `tripId`, then `COMPLETE`. Then `buf curl … TripService/GetTrip -d '{"id":"<tripId>"}'` should return 3 days (Lisbon, Lisbon, Porto), 1 train leg and 2 cities with session ids. The same `buf curl` with `"message":"3 days in Lisbon"` must show no `ROUTE` event and no `stopIndex` anywhere.

---

# Part C — Web (`loci-client`)

Worktree: `cd ~/Work/production/apps/Loci/loci-client && git fetch -q && git worktree add -b feat/multi-city ../loci-client-multicity origin/main && cd ../loci-client-multicity && pnpm install`.

### Task 11: Contract on the client

**Files:**
- Modify: `package.json` (`@buf/loci_loci-proto.bufbuild_es` → the BSR commit from Task 1's `make push`)
- Modify: `src/lib/streaming/chatStream.ts`
- Test: `src/lib/streaming/chatStream.test.ts`

**Interfaces:**
- Produces:
  ```ts
  export interface RouteStop { index: number; cityName: string; cityId?: string; sessionId: string; dayNumbers: number[] }
  export interface RouteLeg { afterDay: number; fromName: string; toName: string; distanceKm: number; durationMins: number; mode: string; fromLat?: number; fromLon?: number; toLat?: number; toLon?: number }
  export interface RouteInfo { stops: RouteStop[]; legs: RouteLeg[]; outline: string; warnings: string[]; dropped: { cityName: string; reason: string }[]; totalTravelMins: number; tripId?: string }
  // LociStreamEvent gains  stopIndex?: number  on every variant, and  | { kind: "route"; route: RouteInfo }
  // ChatStreamParams gains  stops?: { cityName: string; nights?: number }[];  suggestOrder?: boolean
  ```

- [ ] **Step 1: Bump the package.** `pnpm add @buf/loci_loci-proto.bufbuild_es@<version-for-the-new-BSR-commit>`. Get the version string with `pnpm view @buf/loci_loci-proto.bufbuild_es versions --json | tail -5` and pick the one whose suffix matches the commit `make push` printed.

- [ ] **Step 2: Write the failing tests** (append to `chatStream.test.ts`, using the file's existing `create(StreamEventSchema, …)` style)

```ts
import { create } from "@bufbuild/protobuf";
import { StreamEventSchema, StreamEventType, RoutePayloadSchema } from "@buf/loci_loci-proto.bufbuild_es/loci/chat/chat_pb.js";
import { TripLegSchema } from "@buf/loci_loci-proto.bufbuild_es/loci/trip/trip_pb.js";

describe("multi-city events", () => {
  it("maps ROUTE", () => {
    const ev = create(StreamEventSchema, {
      eventType: StreamEventType.ROUTE,
      payload: {
        case: "route",
        value: create(RoutePayloadSchema, {
          stops: [
            { index: 0, cityName: "Lisbon", sessionId: "s0", dayNumbers: [1, 2] },
            { index: 1, cityName: "Porto", sessionId: "s1", dayNumbers: [3] },
          ],
          legs: [create(TripLegSchema, { afterDay: 2, fromName: "Lisbon", toName: "Porto", distanceKm: 274, durationMins: 194, mode: "train" })],
          outline: "Lisbon (2 days) → Porto (1 day)",
          tripId: "t1",
        }),
      },
    });
    const out = mapProtoEvent(ev);
    expect(out?.kind).toBe("route");
    if (out?.kind !== "route") return;
    expect(out.route.stops.map((s) => s.cityName)).toEqual(["Lisbon", "Porto"]);
    expect(out.route.legs[0]).toMatchObject({ afterDay: 2, mode: "train", durationMins: 194 });
    expect(out.route.tripId).toBe("t1");
  });

  it("carries stopIndex on per-city events and keeps single-city events untagged", () => {
    const tagged = mapProtoEvent(create(StreamEventSchema, { stopIndex: 1, payload: { case: "progress", value: { stage: "x" } } }));
    expect(tagged?.stopIndex).toBe(1);
    const plain = mapProtoEvent(create(StreamEventSchema, { payload: { case: "progress", value: { stage: "x" } } }));
    expect(plain?.stopIndex).toBeUndefined();
  });

  it("sends stops and suggestOrder", () => {
    const req = buildRequest({ message: "trip", stops: [{ cityName: "Lisbon", nights: 3 }, { cityName: "Porto" }], suggestOrder: true });
    expect(req.stops.map((s) => [s.cityName, s.nights])).toEqual([["Lisbon", 3], ["Porto", undefined]]);
    expect(req.suggestOrder).toBe(true);
  });
});
```

(If `buildRequest` isn't exported, export it for tests with `export const buildRequest = …`.)

- [ ] **Step 3: Run to verify it fails**

Run: `pnpm vitest run src/lib/streaming/chatStream.test.ts`
Expected: FAIL. `"route"` is unmapped, and `stopIndex`/`stops` are missing.

- [ ] **Step 4: Implement** in `chatStream.ts`:
- Add the `RouteStop`, `RouteLeg` and `RouteInfo` interfaces (see Interfaces above) and export them.
- Change `LociStreamEvent` to `{ eventId?: string; stopIndex?: number } & ( … | { kind: "route"; route: RouteInfo } )`.
- In `mapProtoEvent`, add a case:

```ts
    case "route":
      return {
        kind: "route",
        route: {
          stops: p.value.stops.map((s) => ({
            index: s.index,
            cityName: s.cityName,
            cityId: s.cityId || undefined,
            sessionId: s.sessionId,
            dayNumbers: [...s.dayNumbers],
          })),
          legs: p.value.legs.map((l) => ({
            afterDay: l.afterDay,
            fromName: l.fromName,
            toName: l.toName,
            distanceKm: l.distanceKm,
            durationMins: l.durationMins,
            mode: l.mode,
            fromLat: l.fromLat,
            fromLon: l.fromLon,
            toLat: l.toLat,
            toLon: l.toLon,
          })),
          outline: p.value.outline,
          warnings: [...p.value.warnings],
          dropped: p.value.dropped.map((d) => ({ cityName: d.cityName, reason: d.reason })),
          totalTravelMins: p.value.totalTravelMins,
          tripId: p.value.tripId || undefined,
        },
      };
```

- Wrap the function so every mapped event gets `stopIndex`: rename the current body to `mapPayload(ev)`, and make

```ts
export function mapProtoEvent(ev: ProtoStreamEvent): LociStreamEvent | null {
  const out = mapPayload(ev);
  if (out && ev.stopIndex !== undefined) out.stopIndex = ev.stopIndex;
  return out;
}
```

- In `ChatStreamParams`, add `stops?: { cityName: string; nights?: number }[]; suggestOrder?: boolean;`. In `buildRequest`, add `stops: (params.stops ?? []).map((s) => ({ cityName: s.cityName, nights: s.nights })), suggestOrder: params.suggestOrder ?? false,`.
- The existing event-id dedupe (the "Resume safety" comment) must key on `eventId + kind + stopIndex`. The server reuses event ids across cities, as iOS notes in `SearchState.seen`. Find the dedupe set in this file and extend its key.

- [ ] **Step 5: Run and commit**

Run: `pnpm vitest run src/lib/streaming/ && pnpm typecheck`
Expected: PASS.

```bash
git add package.json pnpm-lock.yaml src/lib/streaming/chatStream.ts src/lib/streaming/chatStream.test.ts
git commit -m "feat(stream): map ROUTE and stop_index; send multi-city stops"
```

### Task 12: Per-city projection in the streaming service

**Files:**
- Create: `src/lib/streaming/multi-city.ts`
- Test: `src/lib/streaming/multi-city.test.ts`
- Modify: `src/lib/streaming-service.ts:199-322` (`project`)
- Modify: `src/lib/api/types.ts:758` (`StreamingSession`)

**Interfaces:**
- Consumes: `LociStreamEvent`, `RouteInfo` (Task 11).
- Produces:
  ```ts
  export interface StopState { index: number; cityName: string; sessionId: string; dayNumbers: number[]; data: Partial<UnifiedChatResponse> | null; error?: string; done: boolean }
  export function applyCityEvent(data: Partial<UnifiedChatResponse> | null, ev: LociStreamEvent, isCity: boolean, sessionId: string): Partial<UnifiedChatResponse> | null  // pure; what project() did per case
  export function stopsFromRoute(route: RouteInfo, prev?: StopState[]): StopState[]
  export function applyStopEvent(stops: StopState[], ev: LociStreamEvent, isCity: boolean): StopState[]
  // StreamingSession gains  route?: RouteInfo; stops?: StopState[]
  ```

- [ ] **Step 1: Write the failing tests** (`multi-city.test.ts`)

```ts
import { describe, expect, it } from "vitest";
import { applyCityEvent, applyStopEvent, stopsFromRoute } from "./multi-city";
import type { RouteInfo } from "./chatStream";

const route: RouteInfo = {
  stops: [
    { index: 0, cityName: "Lisbon", sessionId: "s0", dayNumbers: [1, 2] },
    { index: 1, cityName: "Porto", sessionId: "s1", dayNumbers: [3] },
  ],
  legs: [], outline: "", warnings: [], dropped: [], totalTravelMins: 0,
};

describe("multi-city projection", () => {
  it("routes a tagged event to its city only", () => {
    let stops = stopsFromRoute(route);
    stops = applyStopEvent(stops, { kind: "general_pois", pois: [{ id: "p1", name: "Ribeira" } as any], sessionId: "s1", stopIndex: 1 }, true);
    expect(stops[0].data).toBeNull();
    expect(stops[1].data?.points_of_interest?.[0].name).toBe("Ribeira");
  });

  it("a city's error marks that city, not the others", () => {
    let stops = stopsFromRoute(route);
    stops = applyStopEvent(stops, { kind: "error", userMessage: "Porto failed", internalCode: "x", retryable: true, stopIndex: 1 }, true);
    expect(stops[1].error).toBe("Porto failed");
    expect(stops[0].error).toBeUndefined();
  });

  it("the second ROUTE (with tripId) keeps what each city already has", () => {
    let stops = stopsFromRoute(route);
    stops = applyStopEvent(stops, { kind: "general_pois", pois: [{ id: "p1", name: "Belém" } as any], sessionId: "s0", stopIndex: 0 }, true);
    const again = stopsFromRoute({ ...route, tripId: "t1" }, stops);
    expect(again[0].data?.points_of_interest?.[0].name).toBe("Belém");
  });

  it("applyCityEvent matches the single-city projection", () => {
    const d = applyCityEvent(null, { kind: "city_data", city: { city: "Lisbon" } as any, sessionId: "s0" }, true, "s0");
    expect(d?.general_city_data?.city).toBe("Lisbon");
    expect(d?.session_id).toBe("s0");
  });
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `pnpm vitest run src/lib/streaming/multi-city.test.ts`
Expected: FAIL, module not found.

- [ ] **Step 3: Implement**

`multi-city.ts`: move the per-case data transforms out of `streaming-service.ts` `project()` (the `city_data`, `general_pois`, `itinerary`, `hotels`, `restaurants` and `activities` cases, lines ~243–312) into `applyCityEvent`, keeping identical output:

```ts
import type { LociStreamEvent, RouteInfo } from "./chatStream";
import type {
  AccommodationResponse, ActivitiesResponse, AiCityResponse, DiningResponse,
  HotelDetailedInfo, RestaurantDetailedInfo, UnifiedChatResponse,
} from "~/lib/api/types";

export interface StopState {
  index: number;
  cityName: string;
  sessionId: string;
  dayNumbers: number[];
  data: Partial<UnifiedChatResponse> | null;
  error?: string;
  done: boolean;
}

type Data = Partial<UnifiedChatResponse> | null;

/** One event's effect on one city's result — the single-city projection, unchanged. */
export function applyCityEvent(data: Data, ev: LociStreamEvent, isCity: boolean, sessionId: string): Data {
  switch (ev.kind) {
    case "city_data": {
      if (!isCity || !ev.city) return data;
      return { ...(data ?? {}), general_city_data: ev.city, session_id: sessionId } as Data;
    }
    case "general_pois": {
      if (!isCity) return data;
      const next = { ...(data ?? {}), points_of_interest: ev.pois } as Partial<AiCityResponse>;
      if (ev.city) next.general_city_data = ev.city;
      return next as Data;
    }
    case "itinerary":
      return ev.cityResponse as Data;
    case "hotels":
      return { general_city_data: ev.city, hotels: ev.pois as unknown as HotelDetailedInfo[], domain: "accommodation", session_id: ev.sessionId || sessionId } as AccommodationResponse as Data;
    case "restaurants":
      return { general_city_data: ev.city, restaurants: ev.pois as unknown as RestaurantDetailedInfo[], domain: "dining", session_id: ev.sessionId || sessionId } as DiningResponse as Data;
    case "activities":
      return { general_city_data: ev.city, activities: ev.pois, domain: "activities", session_id: ev.sessionId || sessionId } as ActivitiesResponse as Data;
    default:
      return data;
  }
}

/** The cities a ROUTE names; a repeated ROUTE keeps what each city already streamed. */
export function stopsFromRoute(route: RouteInfo, prev: StopState[] = []): StopState[] {
  return route.stops.map((s) => {
    const had = prev.find((p) => p.index === s.index);
    return { index: s.index, cityName: s.cityName, sessionId: s.sessionId, dayNumbers: s.dayNumbers, data: had?.data ?? null, error: had?.error, done: had?.done ?? false };
  });
}

/** Apply a tagged event to its city. Untagged events are not this function's business. */
export function applyStopEvent(stops: StopState[], ev: LociStreamEvent, isCity: boolean): StopState[] {
  if (ev.stopIndex === undefined) return stops;
  return stops.map((s) => {
    if (s.index !== ev.stopIndex) return s;
    if (ev.kind === "error") return { ...s, error: ev.userMessage, done: true };
    const data = applyCityEvent(s.data, ev, isCity, s.sessionId);
    const done = ev.kind === "itinerary" || ev.kind === "hotels" || ev.kind === "restaurants" || ev.kind === "activities";
    return { ...s, data, done: s.done || done };
  });
}
```

`api/types.ts` `StreamingSession`: add `route?: RouteInfo; stops?: StopState[];` (import the types from `~/lib/streaming/chatStream` and `~/lib/streaming/multi-city`).

`streaming-service.ts` `project()`: at the very top of the switch, before `case "start"`, handle multi-city:

```ts
    if (event.kind === "route") {
      mgr.session.route = event.route;
      mgr.session.stops = stopsFromRoute(event.route, mgr.session.stops);
      if (event.route.tripId) mgr.session.tripId = event.route.tripId;
      this.publish(run);
      mgr.onProgress(mgr.session);
      return;
    }
    if (event.stopIndex !== undefined && mgr.session.stops) {
      // A city of a multi-city trip. Its error is that city's, not the run's.
      mgr.session.stops = applyStopEvent(mgr.session.stops, event, isCity);
      // The first city stands in for `data` so every existing reader of
      // session.data keeps rendering something.
      mgr.session.data = (mgr.session.stops[0]?.data ?? mgr.session.data) as typeof mgr.session.data;
      this.publish(run, { stops: mgr.session.stops, route: mgr.session.route } as Partial<LiveStream>);
      mgr.onProgress(mgr.session);
      return;
    }
```

Then replace each data transform in the existing single-city cases with a call to `applyCityEvent(mgr.session.data, event, isCity, mgr.session.sessionId)`, keeping their `mgr.session.city` updates. Add `route?` and `stops?` to `LiveStream` in `live-stream-store.ts` and to `persistActiveSession`'s envelope, so a reload restores both.

- [ ] **Step 4: Run**

Run: `pnpm vitest run src/lib/ && pnpm typecheck`
Expected: PASS, including the existing `streaming-service.test.ts` and `streaming-state.test.ts`, which pin the single-city behaviour.

- [ ] **Step 5: Commit**

```bash
git add src/lib/streaming/multi-city.ts src/lib/streaming/multi-city.test.ts src/lib/streaming-service.ts src/lib/api/types.ts src/lib/streaming/live-stream-store.ts
git commit -m "feat(stream): project each city of a multi-city run into its own state"
```

### Task 13: Itinerary page — stop switcher, all-days view, legs, map

**Files:**
- Modify: `src/lib/hooks/useStreamedRpc.ts`
- Create: `src/components/features/MultiCity/StopSwitcher.tsx`, `LegRow.tsx`, `multi-city-view.ts`, `multi-city-view.test.ts`
- Modify: `src/routes/itinerary/index.tsx`

**Interfaces:**
- Consumes: `StopState`, `RouteInfo`, `RouteLeg`.
- Produces:
  ```ts
  // useStreamedRpc(message, cityName, profileId, opts, stops?: () => StopInput[] | undefined, suggestOrder?: () => boolean)
  // store gains  route: RouteInfo | null; stops: StopState[]
  export type StopInput = { cityName: string; nights?: number }
  export function parseStopsParam(raw: string | undefined): StopInput[]      // "Lisbon:3,Porto:2,Seville" → [...]
  export function formatStopsParam(stops: StopInput[]): string
  export function stopChipLabel(s: StopState): string                         // "Lisbon · 2n"
  export function allDaysTimeline(stops: StopState[], legs: RouteLeg[]): TimelineItem[]
  export type TimelineItem = { kind: "day"; day: number; cityName: string; stopIndex: number; pois: POIDetailedInfo[] } | { kind: "leg"; leg: RouteLeg }
  export function formatLeg(leg: RouteLeg): string                            // "Train · ≈3h14 · 274 km"
  ```

- [ ] **Step 1: Write the failing tests** (`multi-city-view.test.ts`)

```ts
import { describe, expect, it } from "vitest";
import { allDaysTimeline, formatLeg, formatStopsParam, parseStopsParam, stopChipLabel } from "./multi-city-view";

describe("multi-city view helpers", () => {
  it("parses and formats the stops param", () => {
    expect(parseStopsParam("Lisbon:3,Porto:2,São Paulo")).toEqual([
      { cityName: "Lisbon", nights: 3 }, { cityName: "Porto", nights: 2 }, { cityName: "São Paulo", nights: undefined },
    ]);
    expect(parseStopsParam(undefined)).toEqual([]);
    expect(parseStopsParam("a,b,c,d,e,f")).toHaveLength(5);
    expect(formatStopsParam([{ cityName: "Lisbon", nights: 3 }, { cityName: "Porto" }])).toBe("Lisbon:3,Porto");
  });

  it("labels chips and legs", () => {
    expect(stopChipLabel({ index: 0, cityName: "Lisbon", sessionId: "s", dayNumbers: [1, 2], data: null, done: false })).toBe("Lisbon · 2n");
    expect(formatLeg({ afterDay: 2, fromName: "Lisbon", toName: "Porto", distanceKm: 274.4, durationMins: 194, mode: "train" })).toBe("Train · ≈3h14 · 274 km");
  });

  it("interleaves days and legs in trip order", () => {
    const items = allDaysTimeline(
      [
        { index: 0, cityName: "Lisbon", sessionId: "s0", dayNumbers: [1, 2], done: true,
          data: { itinerary_response: { points_of_interest: [{ name: "Belém", day: 1 } as any, { name: "Alfama", day: 2 } as any] } } as any },
        { index: 1, cityName: "Porto", sessionId: "s1", dayNumbers: [3], done: true,
          data: { itinerary_response: { points_of_interest: [{ name: "Ribeira", day: 1 } as any] } } as any },
      ],
      [{ afterDay: 2, fromName: "Lisbon", toName: "Porto", distanceKm: 274, durationMins: 194, mode: "train" }],
    );
    expect(items.map((i) => (i.kind === "day" ? `${i.cityName}${i.day}` : "leg"))).toEqual(["Lisbon1", "Lisbon2", "leg", "Porto3"]);
    const porto = items[3];
    expect(porto.kind === "day" && porto.pois[0].name).toBe("Ribeira");
  });
});
```

- [ ] **Step 2: Run to verify it fails.** `pnpm vitest run src/components/features/MultiCity/`. Expected: FAIL, module missing.

- [ ] **Step 3: Implement**

`multi-city-view.ts`:

```ts
import type { RouteLeg } from "~/lib/streaming/chatStream";
import type { StopState } from "~/lib/streaming/multi-city";
import type { POIDetailedInfo } from "~/lib/api/types";

export type StopInput = { cityName: string; nights?: number };
export type TimelineItem =
  | { kind: "day"; day: number; cityName: string; stopIndex: number; pois: POIDetailedInfo[] }
  | { kind: "leg"; leg: RouteLeg };

export const MAX_STOPS = 5;

export function parseStopsParam(raw: string | undefined): StopInput[] {
  if (!raw) return [];
  return raw
    .split(",")
    .map((part) => {
      const [name, n] = part.split(":");
      const nights = n ? Number.parseInt(n, 10) : Number.NaN;
      return { cityName: decodeURIComponent(name).trim(), nights: Number.isFinite(nights) && nights > 0 ? nights : undefined };
    })
    .filter((s) => s.cityName)
    .slice(0, MAX_STOPS);
}

export const formatStopsParam = (stops: StopInput[]): string =>
  stops.map((s) => (s.nights ? `${s.cityName}:${s.nights}` : s.cityName)).join(",");

export const stopChipLabel = (s: StopState): string => `${s.cityName} · ${s.dayNumbers.length}n`;

const MODE_LABEL: Record<string, string> = { drive: "Drive", train: "Train", bus: "Bus", flight: "Flight" };

const hm = (mins: number) => (mins < 60 ? `${mins} min` : `${Math.floor(mins / 60)}h${String(mins % 60).padStart(2, "0")}`);

export const formatLeg = (leg: RouteLeg): string =>
  `${MODE_LABEL[leg.mode] ?? "Travel"} · ≈${hm(leg.durationMins)} · ${Math.round(leg.distanceKm)} km`;

/** Every day of the trip, across cities, with the move between cities where it happens. */
export function allDaysTimeline(stops: StopState[], legs: RouteLeg[]): TimelineItem[] {
  const items: TimelineItem[] = [];
  for (const stop of stops) {
    const pois = (stop.data as any)?.itinerary_response?.points_of_interest ?? (stop.data as any)?.points_of_interest ?? [];
    stop.dayNumbers.forEach((day, k) => {
      items.push({ kind: "day", day, cityName: stop.cityName, stopIndex: stop.index, pois: pois.filter((p: POIDetailedInfo) => (p as any).day === k + 1) });
      const leg = legs.find((l) => l.afterDay === day && l.fromName === stop.cityName);
      if (leg) items.push({ kind: "leg", leg });
    });
  }
  return items;
}
```

`StopSwitcher.tsx`, with the existing chip styling (copy the class names from the trending presets chips in `discover.tsx:389`):

```tsx
import { For, Show } from "solid-js";
import type { StopState } from "~/lib/streaming/multi-city";
import { stopChipLabel } from "./multi-city-view";

export function StopSwitcher(props: {
  stops: StopState[];
  active: number | "all";
  onSelect: (i: number | "all") => void;
  showAll?: boolean;
}) {
  return (
    <nav class="flex gap-2 overflow-x-auto pb-1" aria-label="Cities in this trip">
      <Show when={props.showAll}>
        <button type="button" class="loci-chip" aria-pressed={props.active === "all"} onClick={() => props.onSelect("all")}>
          All days
        </button>
      </Show>
      <For each={props.stops}>
        {(s, i) => (
          <>
            <Show when={i() > 0}>
              <span aria-hidden="true" class="self-center text-muted">→</span>
            </Show>
            <button type="button" class="loci-chip" aria-pressed={props.active === s.index} onClick={() => props.onSelect(s.index)}>
              {stopChipLabel(s)}
              <Show when={!s.done && !s.error}> <span class="loci-dot-pulse" aria-label="generating" /></Show>
              <Show when={s.error}> <span aria-label="failed">⚠︎</span></Show>
            </button>
          </>
        )}
      </For>
    </nav>
  );
}
```

`LegRow.tsx`:

```tsx
import type { RouteLeg } from "~/lib/streaming/chatStream";
import { formatLeg } from "./multi-city-view";

export function LegRow(props: { leg: RouteLeg }) {
  return (
    <div class="flex items-center gap-3 rounded-xl border border-dashed px-4 py-3 text-sm" role="note">
      <span class="font-medium">{props.leg.fromName} → {props.leg.toName}</span>
      <span class="text-muted">{formatLeg(props.leg)}</span>
      <span class="ml-auto text-xs text-muted">Estimate</span>
    </div>
  );
}
```

Check `MultiCityPlanCard.tsx:95-106` first. If its leg row can take a `RouteLeg`, reuse it and delete `LegRow.tsx`.

`useStreamedRpc.ts`: add the params `stops: () => StopInput[] | undefined = () => undefined` and `suggestOrder: () => boolean = () => false`. Change the guard to `if (!message() || (!cityName() && (stops()?.length ?? 0) < 2)) return;`. Pass `stops: stops(), suggestOrder: suggestOrder()` to `startStream`. Add `route: RouteInfo | null` and `stops: StopState[]` to the store. In `mirror` and in `onComplete`, `setStore("route", s.route ?? null); setStore("stops", s.stops ?? [])`.

`itinerary/index.tsx`:
- `const [stopsParam] = createSignal(parseStopsParam(searchParams.stops as string | undefined));` and `const [suggest] = createSignal(searchParams.suggest === "1");`. Pass both to `useStreamedRpc`.
- `const [activeStop, setActiveStop] = createSignal<number | "all">("all");`
- `const isMulti = () => (store.stops?.length ?? 0) >= 2;`
- `const shown = createMemo(() => (isMulti() && activeStop() !== "all" ? store.stops[activeStop() as number]?.data : store.data));`. Every existing read of `store.data` for rendering the day list, the map POIs and the weather goes through `shown()` instead. Grep the file for `store.data` and switch the render reads only.
- Above the day list: `<Show when={isMulti()}><StopSwitcher stops={store.stops} active={activeStop()} onSelect={setActiveStop} showAll /><p class="text-sm text-muted">{store.route?.outline}</p></Show>`
- When `activeStop() === "all"`, render `allDaysTimeline(store.stops, store.route?.legs ?? [])`. A `day` item uses the page's existing day-section component with that day's POIs and a "Day N · City" header, and a `leg` item renders `<LegRow leg={…}/>`.
- Dropped cities: `<Show when={store.route?.dropped.length}>` with a small note listing `cityName — reason`.
- A stop with `error` shows the page's existing error-state component, scoped to that city, with a Retry that navigates to `/itinerary?message=…&cityName=<city>` (a single-city retry).
- Map: when `activeStop() === "all"`, feed the map every stop's POIs (`store.stops.flatMap(…)`) and set its centre from the bounds of all POIs instead of `mapPois()[0]`. If the map component takes `center` only, compute the midpoint of the min/max lat/lon and use zoom 6 for multi-city.

- [ ] **Step 4: Run.** `pnpm vitest run && pnpm typecheck && pnpm lint`. Expected: PASS.

- [ ] **Step 5: Browser check.** `pnpm dev`, sign in, and open `/itinerary?message=trip&stops=Lisbon:2,Porto:1`. Check that the chips appear, both cities fill in, "All days" shows Lisbon 1, Lisbon 2, the leg card, then Porto 3, and the map fits both cities.

- [ ] **Step 6: Commit**

```bash
git add src/lib/hooks/useStreamedRpc.ts src/components/features/MultiCity/ src/routes/itinerary/index.tsx
git commit -m "feat(itinerary): multi-city trips — city switcher, all-days timeline with legs, map across cities"
```

### Task 14: Stop builder on discover, and multi-city runs started from chat text

**Files:**
- Create: `src/components/features/MultiCity/StopBuilder.tsx`, `stop-builder.ts`, `stop-builder.test.ts`
- Modify: `src/routes/discover.tsx`

**Interfaces:**
- Consumes: `StopInput`, `formatStopsParam`, `MAX_STOPS` (Task 13).
- Produces: `export function addStop(list: StopInput[], name: string): StopInput[]`, `moveStop(list, from, to)`, `setNights(list, i, n)`, `removeStop(list, i)`. All are pure, capped at `MAX_STOPS`, and dedupe names case-insensitively.

- [ ] **Step 1: Failing tests** (`stop-builder.test.ts`)

```ts
import { describe, expect, it } from "vitest";
import { addStop, moveStop, removeStop, setNights } from "./stop-builder";

describe("stop builder", () => {
  it("adds, dedupes and caps", () => {
    let l = addStop([], "Lisbon");
    l = addStop(l, " lisbon ");
    expect(l).toHaveLength(1);
    for (const c of ["Porto", "Seville", "Madrid", "Coimbra", "Faro"]) l = addStop(l, c);
    expect(l).toHaveLength(5);
  });
  it("moves, sets nights within 1..14, removes", () => {
    let l = [{ cityName: "A" }, { cityName: "B" }, { cityName: "C" }];
    l = moveStop(l, 2, 0);
    expect(l.map((s) => s.cityName)).toEqual(["C", "A", "B"]);
    expect(setNights(l, 0, 0)[0].nights).toBe(1);
    expect(setNights(l, 0, 99)[0].nights).toBe(14);
    expect(removeStop(l, 1).map((s) => s.cityName)).toEqual(["C", "B"]);
  });
});
```

- [ ] **Step 2: Run to verify it fails.** `pnpm vitest run src/components/features/MultiCity/stop-builder.test.ts`

- [ ] **Step 3: Implement**

`stop-builder.ts`:

```ts
import { MAX_STOPS, type StopInput } from "./multi-city-view";

const key = (s: string) => s.trim().toLowerCase();

export function addStop(list: StopInput[], name: string): StopInput[] {
  const cityName = name.trim();
  if (!cityName || list.length >= MAX_STOPS || list.some((s) => key(s.cityName) === key(cityName))) return list;
  return [...list, { cityName, nights: 2 }];
}

export function moveStop(list: StopInput[], from: number, to: number): StopInput[] {
  if (from === to || from < 0 || to < 0 || from >= list.length || to >= list.length) return list;
  const next = [...list];
  const [item] = next.splice(from, 1);
  next.splice(to, 0, item);
  return next;
}

export const setNights = (list: StopInput[], i: number, n: number): StopInput[] =>
  list.map((s, j) => (j === i ? { ...s, nights: Math.min(14, Math.max(1, Math.round(n))) } : s));

export const removeStop = (list: StopInput[], i: number): StopInput[] => list.filter((_, j) => j !== i);
```

`StopBuilder.tsx`: a disclosure under the discover location field labelled "Plan several cities". It holds:
- a text input with an "Add" button, calling `addStop`;
- a list of rows, each with the city name, a −/+ nights stepper (`setNights`), up/down buttons (`moveStop`, which is keyboard-accessible, unlike drag), and remove;
- a "Suggest the best order" checkbox;
- a "Plan trip" button, disabled below 2 stops. It calls `props.onPlan(stops, suggest)`.

Write it with the same input and button classes as the discover form (`discover.tsx:505-560`). Keep state in `createSignal<StopInput[]>([])`.

`discover.tsx`:
- Render `<StopBuilder onPlan={(stops, suggest) => navigate(`/itinerary?message=${encodeURIComponent(searchQuery().trim() || "trip")}&stops=${encodeURIComponent(formatStopsParam(stops))}${suggest ? "&suggest=1" : ""}`)} />` below the location input.
- In the inline stream's event switch, add `case "route":`. Set the progress message to `event.route.outline` and remember `multiRoute = event.route`. In `case "complete"`, when `multiRoute` is set and `event.tripId` exists, set `setPersistedTripId(event.tripId)` and `setPersistedTripCity(multiRoute.stops.map((s) => s.cityName).join(" + "))`. The existing persisted-trip CTA then links to the trip. Also add a link "Open the full plan" to `/itinerary?sessionId=${multiRoute.stops[0].sessionId}&tripId=${event.tripId}`.
- Tagged `general_pois`/`itinerary` events must not replace the result list with each city in turn. When `event.stopIndex !== undefined`, **append** to `searchResults` and prefix a city label instead of replacing.

- [ ] **Step 4: Run.** `pnpm vitest run && pnpm typecheck && pnpm lint`

- [ ] **Step 5: Browser check.** On `/discover`, type "Lisbon for 2 days then Porto for 1" and submit. Check that the progress shows the outline, results from both cities appear, and at the end "Open the full plan" works. Then use the builder: add Lisbon, Porto and Seville, tick suggest, and press Plan trip. The itinerary page should open with 3 chips.

- [ ] **Step 6: Commit**

```bash
git add src/components/features/MultiCity/StopBuilder.tsx src/components/features/MultiCity/stop-builder.ts src/components/features/MultiCity/stop-builder.test.ts src/routes/discover.tsx
git commit -m "feat(discover): stop builder and multi-city runs from typed requests"
```

### Task 15: Hotels, restaurants and activities per city

**Files:**
- Modify: `src/routes/hotels/index.tsx`, `src/routes/restaurants/index.tsx`, `src/routes/activities/index.tsx` (they share `lib/results/domain.ts`)
- Modify: `src/lib/results/domain.ts` (a helper)
- Test: `src/lib/results/domain.test.ts` (create it, or extend it if present)

**Interfaces:**
- Produces: `export function stopResults(stops: StopState[] | undefined, active: number, domain: ResultsDomain): UnwrappedResults | null`, which is `unwrapDomainResults(stops[active].data, domain)`.

- [ ] **Step 1: Failing test**

```ts
import { describe, expect, it } from "vitest";
import { stopResults } from "./domain";

it("unwraps the active city's hotels", () => {
  const stops = [
    { index: 0, cityName: "Lisbon", sessionId: "s0", dayNumbers: [1], done: true, data: { hotels: [{ id: "h1", name: "Lisbon Inn" }] } as any },
    { index: 1, cityName: "Porto", sessionId: "s1", dayNumbers: [2], done: true, data: { hotels: [{ id: "h2", name: "Porto Inn" }] } as any },
  ];
  expect(stopResults(stops, 1, "hotels")?.items?.[0]?.name).toBe("Porto Inn");
  expect(stopResults(undefined, 0, "hotels")).toBeNull();
});
```

(Adjust `.items` to `UnwrappedResults`'s real field name; check `domain.ts:78`.)

- [ ] **Step 2: Run to verify it fails.** `pnpm vitest run src/lib/results/`

- [ ] **Step 3: Implement.** In `domain.ts`:

```ts
export function stopResults(stops: StopState[] | undefined, active: number, domain: ResultsDomain): UnwrappedResults | null {
  const stop = stops?.find((s) => s.index === active);
  return stop?.data ? unwrapDomainResults(stop.data, domain) : null;
}
```

In each of the three routes, read `live.stops` (from the same live session they already read). When there are two or more, render `<StopSwitcher stops={…} active={active()} onSelect={setActive} />` (no "All" option) above the list, and feed the list from `stopResults(…)` instead of the single unwrap. The title uses `listTitle(domain, stops[active].cityName)`.

- [ ] **Step 4: Run.** `pnpm vitest run && pnpm typecheck`

- [ ] **Step 5: Browser check.** Discover → "hotels in Lisbon and Porto". Check that `/hotels` shows two chips and each lists its own city's hotels.

- [ ] **Step 6: Commit.** `git add src/lib/results/domain.ts src/lib/results/domain.test.ts src/routes/hotels/index.tsx src/routes/restaurants/index.tsx src/routes/activities/index.tsx && git commit -m "feat(results): per-city hotels, restaurants and activities in multi-city runs"`

### Task 16: Save, offline, share and reopen

**Files:**
- Modify: `src/lib/itinerary-offline-store.ts` (the payload union)
- Modify: `src/lib/saved-itineraries.ts` (multi entries)
- Modify: `src/routes/itinerary/index.tsx` (the save/share handlers ~`:520-560`; reopen by `tripId`)
- Test: `src/lib/itinerary-offline-store.test.ts` / `src/lib/saved-itineraries.test.ts` (extend them)

**Interfaces:**
- Produces:
  ```ts
  export type OfflinePayload = AiCityResponse | { kind: "multi"; route: RouteInfo; stops: { cityName: string; sessionId: string; dayNumbers: number[]; data: Partial<UnifiedChatResponse> | null }[] }
  export const isMultiPayload = (p: OfflinePayload): p is Extract<OfflinePayload, { kind: "multi" }>
  export function multiShareText(route: RouteInfo, stops: StopState[]): string
  // SavedItinerary gains  cities?: string[]  and  tripId?: string
  ```

- [ ] **Step 1: Failing tests**

```ts
import { multiShareText } from "~/components/features/MultiCity/multi-city-view";

it("share text groups by city, then day, and credits Loci", () => {
  const text = multiShareText(
    { stops: [], legs: [{ afterDay: 1, fromName: "Lisbon", toName: "Porto", distanceKm: 274, durationMins: 194, mode: "train" }], outline: "Lisbon (1 day) → Porto (1 day)", warnings: [], dropped: [], totalTravelMins: 194 },
    [
      { index: 0, cityName: "Lisbon", sessionId: "s0", dayNumbers: [1], done: true, data: { itinerary_response: { points_of_interest: [{ name: "Belém", day: 1 }] } } as any },
      { index: 1, cityName: "Porto", sessionId: "s1", dayNumbers: [2], done: true, data: { itinerary_response: { points_of_interest: [{ name: "Ribeira", day: 1 }] } } as any },
    ],
  );
  expect(text).toMatch(/^Lisbon \(1 day\) → Porto \(1 day\)/);
  expect(text.indexOf("Lisbon")).toBeLessThan(text.indexOf("Train"));
  expect(text.indexOf("Train")).toBeLessThan(text.indexOf("Porto\n"));
  expect(text).toContain("Day 2 — Ribeira");
  expect(text.trim().endsWith("Generated from Loci")).toBe(true);
});
```

In `saved-itineraries.test.ts`, check that `mergeSavedItineraries` turns an offline multi entry into `{ cityName: "Lisbon + Porto", cities: ["Lisbon", "Porto"], tripId }` and that it doesn't collapse into a single-city entry for Lisbon.

- [ ] **Step 2: Run to verify it fails.**

- [ ] **Step 3: Implement**
- In `multi-city-view.ts`, add:

```ts
export function multiShareText(route: RouteInfo, stops: StopState[]): string {
  const lines: string[] = [route.outline, ""];
  for (const item of allDaysTimeline(stops, route.legs)) {
    if (item.kind === "leg") {
      lines.push(`${item.leg.fromName} → ${item.leg.toName}: ${formatLeg(item.leg)}`, "");
      continue;
    }
    const prev = lines[lines.length - 2];
    if (!prev?.startsWith(item.cityName)) lines.push(item.cityName);
    lines.push(`Day ${item.day} — ${item.pois.map((p) => p.name).join(", ") || "Free day"}`);
  }
  lines.push("", "Generated from Loci");
  return lines.join("\n");
}
```

  Match the single-city share text's existing footer string exactly by grepping for "Generated from Loci".
- In `itinerary-offline-store.ts`, widen `payload` to `OfflinePayload`. Add `isMultiPayload`. Leave `DB_VERSION` alone, because the change is structural only.
- In `itinerary/index.tsx`, when `isMulti()`:
  - **Save** writes `{ id: store.tripId ?? store.stops[0].sessionId, title: route.stops.map(s=>s.cityName).join(" + "), cityName: <same>, payload: { kind: "multi", route, stops } }`. The account bookmark goes to the parent trip, because the trip is already saved server-side: mark it saved by linking to `/trips/${tripId}` rather than calling `BookmarkItinerary` per city.
  - **Share** uses `multiShareText`.
  - The **JSON export** is named `trip-${cities.join("-")}.json`.
- **Reopen:** `/itinerary?tripId=…` with no live run fetches `TripService.GetTrip`. If `cities.length >= 2`, it hydrates each city via the existing `hydrate-session.ts` (by `session_id`) into `store.stops`, and builds `store.route` from the trip's legs and cities. An offline multi payload hydrates straight from IndexedDB with no network. `/saved` links multi entries to `/itinerary?tripId=…`.

- [ ] **Step 4: Run.** `pnpm vitest run && pnpm typecheck && pnpm lint`

- [ ] **Step 5: Browser check.**
  - Save a 2-city trip. `/saved` should show "Lisbon + Porto".
  - Go offline in DevTools and reopen it: both cities should render.
  - Share: the copied text should be grouped by city.
  - Reload `/itinerary?tripId=…`: it should rehydrate.

- [ ] **Step 6: Commit and PR**

```bash
git add src/lib/itinerary-offline-store.ts src/lib/saved-itineraries.ts src/lib/saved-itineraries.test.ts src/components/features/MultiCity/multi-city-view.ts src/components/features/MultiCity/multi-city-view.test.ts src/routes/itinerary/index.tsx src/routes/saved/index.tsx
git commit -m "feat(saved): save, share, reopen and offline for multi-city trips"
git push -u origin feat/multi-city && gh pr create --title "Multi-city trips (web)" --fill
```

(Format only the touched paths: `pnpm oxfmt <files>`. Never run a repo-wide format.)

---

# Part D — iOS (`loci-ios`)

Worktree: `cd ~/Work/production/apps/Loci/loci-ios && git fetch -q && git worktree add -b feat/multi-city ../loci-ios-multicity origin/main`. The Xcode project's local package path `../../loci-connect-proto` resolves from `loci-ios-multicity/loci/` to `Loci/loci-connect-proto`, which **must be on the v5.26.0 commit** (Task 1, Step 5). Before editing `SearchSessionController.swift`, `SearchResultsView.swift` or `project.pbxproj`, message north-loci-onboarding-verdict. Its unpushed branches touch those files.

### Task 17: SearchState understands multi-city streams

**Files:**
- Modify: `loci/loci/Features/Search/Model/SearchState.swift`
- Test: `loci/lociTests/SearchStateTests.swift`

**Interfaces:**
- Produces:
  ```swift
  nonisolated struct StopResult: Equatable, Sendable { var index: Int; var cityName: String; var sessionId: String; var dayNumbers: [Int]; var state: SearchState; var error: String? }
  // SearchState gains:
  var route: Loci_Chat_RoutePayload?
  var stops: [StopResult] = []
  var isMultiCity: Bool { stops.count >= 2 }
  ```
  `apply(_:)` does three things for multi-city events:
  - A `.route` payload sets `route` and builds or refreshes `stops`, keeping each city's state.
  - An event with `hasStopIndex` goes to `stops[i].state.apply(event)`. An `.error` there sets `stops[i].error` and never changes the parent `status`.
  - An untagged event behaves exactly as it does today.

- [ ] **Step 1: Failing tests** (append to `SearchStateTests.swift`, using the file's existing event-builder helpers)

```swift
func testRouteBuildsStopsAndTaggedEventsGoToTheirCity() {
  var state = SearchState()
  state.apply(.with { $0.payload = .start(.with { $0.sessionID = "s0" }) })
  state.apply(.with {
    $0.payload = .route(.with {
      $0.stops = [
        .with { $0.index = 0; $0.cityName = "Lisbon"; $0.sessionID = "s0"; $0.dayNumbers = [1, 2] },
        .with { $0.index = 1; $0.cityName = "Porto"; $0.sessionID = "s1"; $0.dayNumbers = [3] },
      ]
      $0.outline = "Lisbon (2 days) → Porto (1 day)"
    })
  })
  XCTAssertTrue(state.isMultiCity)

  state.apply(.with {
    $0.stopIndex = 1
    $0.payload = .generalPois(.with { $0.pois = [.with { $0.name = "Ribeira" }] })
  })
  XCTAssertTrue(state.stops[0].state.generalPOIs.isEmpty)
  XCTAssertEqual(state.stops[1].state.generalPOIs.first?.name, "Ribeira")
}

func testCityErrorDoesNotFailTheSearch() {
  var state = SearchState()
  state.apply(.with { $0.payload = .start(.with { $0.sessionID = "s0" }) })
  state.apply(.with { $0.payload = .route(.with { $0.stops = [.with { $0.index = 0; $0.cityName = "A" }, .with { $0.index = 1; $0.cityName = "B" }] }) })
  let effect = state.apply(.with { $0.stopIndex = 1; $0.payload = .error(.with { $0.userMessage = "B failed" }) })
  XCTAssertNil(effect)
  XCTAssertEqual(state.status, .streaming)
  XCTAssertEqual(state.stops[1].error, "B failed")
}

func testSecondRouteKeepsCityResults() {
  var state = SearchState()
  let route: Loci_Chat_StreamEvent = .with { $0.payload = .route(.with { $0.stops = [.with { $0.index = 0; $0.cityName = "A" }, .with { $0.index = 1; $0.cityName = "B" }] }) }
  state.apply(route)
  state.apply(.with { $0.stopIndex = 0; $0.payload = .generalPois(.with { $0.pois = [.with { $0.name = "X" }] }) })
  state.apply(.with { $0.eventID = "r2"; $0.payload = .route(.with { $0.stops = route.route.stops; $0.tripID = "t1" }) })
  XCTAssertEqual(state.stops[0].state.generalPOIs.first?.name, "X")
  XCTAssertEqual(state.route?.tripID, "t1")
}

func testSingleCityStreamUnchanged() {
  var state = SearchState()
  state.apply(.with { $0.payload = .generalPois(.with { $0.pois = [.with { $0.name = "Y" }] }) })
  XCTAssertFalse(state.isMultiCity)
  XCTAssertEqual(state.generalPOIs.first?.name, "Y")
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `xcodebuild test -project loci/loci.xcodeproj -scheme loci -destination 'platform=iOS Simulator,name=iPhone 17' -only-testing:lociTests/SearchStateTests 2>&1 | xcpretty || true`
Expected: FAIL, `value of type 'SearchState' has no member 'isMultiCity'`. Check with `xcrun simctl list` which simulator is free; other sessions use iPhone 17 Pro and iPhone Air.

- [ ] **Step 3: Implement.** In `SearchState.swift`:

```swift
/// One city of a multi-city search: the same state a single-city search
/// builds, per city.
nonisolated struct StopResult: Equatable, Sendable {
  var index: Int
  var cityName: String
  var sessionId: String
  var dayNumbers: [Int]
  var state = SearchState()
  var error: String?
}
```

Add to `SearchState`:

```swift
  /// A multi-city search's route (chat.proto RoutePayload); nil for one city.
  var route: Loci_Chat_RoutePayload?
  /// Each city's own results, in route order.
  var stops: [StopResult] = []
  var isMultiCity: Bool { stops.count >= 2 }
```

In `apply(_:)`, right after the dedupe guard. Include the stop index in the dedupe key: `let key = "\(event.eventID)|\(payload.caseName)|\(event.hasStopIndex ? String(event.stopIndex) : "-")"`.

```swift
    if case .route(let route) = payload {
      self.route = route
      stops = route.stops.map { ref in
        var stop = stops.first { $0.index == Int(ref.index) }
          ?? StopResult(index: Int(ref.index), cityName: ref.cityName, sessionId: ref.sessionID, dayNumbers: ref.dayNumbers.map(Int.init))
        stop.cityName = ref.cityName
        stop.sessionId = ref.sessionID
        stop.dayNumbers = ref.dayNumbers.map(Int.init)
        stop.state.cityName = ref.cityName
        return stop
      }
      return nil
    }
    if event.hasStopIndex, let i = stops.firstIndex(where: { $0.index == Int(event.stopIndex) }) {
      if case .error(let error) = payload {
        stops[i].error = error.userMessage.isEmpty ? "This city failed." : error.userMessage
        return nil
      }
      var inner = event
      inner.clearStopIndex()
      inner.eventID = ""   // the parent already de-duplicated it
      stops[i].state.apply(inner)
      // The first city stands in for the flat fields, so every existing view has something.
      if i == 0 { adoptFirstStop(stops[0].state) }
      return nil
    }
```

Then:

```swift
  private mutating func adoptFirstStop(_ s: SearchState) {
    if let it = s.itinerary { itinerary = it }
    if !s.generalPOIs.isEmpty { generalPOIs = s.generalPOIs }
    if !s.hotels.isEmpty { hotels = s.hotels }
    if !s.restaurants.isEmpty { restaurants = s.restaurants }
    if !s.activities.isEmpty { activities = s.activities }
    if let c = s.cityData { cityData = c }
  }
```

Add `case .route: "route"` to `caseName`. `SearchState` now contains `[StopResult]`, which in turn contains `SearchState`. Arrays can hold a recursive value type, so this compiles; if the compiler complains, box it with `indirect enum`.

- [ ] **Step 4: Run.** Same command. Expected: PASS for all of `SearchStateTests`.

- [ ] **Step 5: Commit.** `git add loci/loci/Features/Search/Model/SearchState.swift loci/lociTests/SearchStateTests.swift && git commit -m "feat(search): SearchState follows multi-city streams per city"`

### Task 18: Start a multi-city search; persist it

**Files:**
- Modify: `loci/loci/Features/Search/SearchSessionController.swift` (`start(...)` ~:60, request build ~:373)
- Modify: `loci/loci/Features/Search/Model/SearchEnvelope.swift`
- Test: `loci/lociTests/MultiCityTests.swift`

**Interfaces:**
- Produces: `struct StopInput: Codable, Equatable, Sendable { var cityName: String; var nights: Int? }`. The signature becomes `start(query:cityName:stops: [StopInput] = [], suggestOrder: Bool = false, …)`. `SearchEnvelope` gains `var stops: [StopInput]? ; var suggestOrder: Bool?`. The request sets `request.stops` and `request.suggestOrder`.

- [ ] **Step 1: Failing test** (`MultiCityTests.swift`)

```swift
import XCTest
@testable import loci

final class MultiCityTests: XCTestCase {
  func testEnvelopeBuildsStopsIntoTheRequest() {
    var envelope = SearchEnvelope(sessionId: nil, requestId: "r", profileId: nil, lastEventId: nil, query: "trip",
      cityName: nil, domain: nil, latitude: nil, longitude: nil, startedAt: .now, finished: false, notified: false)
    envelope.stops = [StopInput(cityName: "Lisbon", nights: 3), StopInput(cityName: "Porto", nights: nil)]
    envelope.suggestOrder = true
    let request = SearchSessionController.makeRequest(from: envelope)
    XCTAssertEqual(request.stops.map(\.cityName), ["Lisbon", "Porto"])
    XCTAssertEqual(request.stops[0].nights, 3)
    XCTAssertFalse(request.stops[1].hasNights)
    XCTAssertTrue(request.suggestOrder)
  }

  func testOldEnvelopesStillDecode() throws {
    let json = #"{"requestId":"r","query":"q","startedAt":0,"finished":false,"notified":false}"#
    let env = try JSONDecoder().decode(SearchEnvelope.self, from: Data(json.utf8))
    XCTAssertNil(env.stops)
  }
}
```

(If the request is built inline at `:373`, extract it into `static func makeRequest(from envelope: SearchEnvelope) -> Loci_Chat_ChatRequest`. That is a small refactor that makes it testable.)

- [ ] **Step 2: Run to verify it fails.** `-only-testing:lociTests/MultiCityTests`

- [ ] **Step 3: Implement.** Add `StopInput` to `SearchEnvelope.swift`, plus the two optional fields. Optional fields keep old files decodable. In `makeRequest`:

```swift
    for stop in envelope.stops ?? [] {
      var s = Loci_Chat_TripStopInput()
      s.cityName = stop.cityName
      if let n = stop.nights { s.nights = Int32(n) }
      request.stops.append(s)
    }
    request.suggestOrder = envelope.suggestOrder ?? false
```

`start(...)` gains `stops: [StopInput] = [], suggestOrder: Bool = false` and writes both into the envelope it creates. Everything else is unchanged.

- [ ] **Step 4: Run.** Expected: PASS.

- [ ] **Step 5: Commit.** `git add loci/loci/Features/Search/SearchSessionController.swift loci/loci/Features/Search/Model/SearchEnvelope.swift loci/lociTests/MultiCityTests.swift && git commit -m "feat(search): start and resume multi-city searches"`

### Task 19: Stop builder, city picker, legs on the results page

**Files:**
- Create: `loci/loci/Features/Search/UI/MultiCity/StopBuilderSheet.swift`, `StopPicker.swift`, `LegRowView.swift`, `MultiCityFormat.swift`
- Modify: `loci/loci/Features/Search/UI/Results/ResultsPage.swift`, the composer (`SearchComposer.swift`, which gets a "Several cities" button)
- Test: `loci/lociTests/MultiCityTests.swift` (formatting)

**Interfaces:**
- Produces: `enum MultiCityFormat { static func leg(_ leg: Loci_Trip_TripLeg) -> String; static func chip(_ stop: StopResult) -> String }`, which match web's `formatLeg`/`stopChipLabel` exactly ("Train · ≈3h14 · 274 km", "Lisbon · 2n").

- [ ] **Step 1: Failing tests** (append)

```swift
  func testFormatMatchesWeb() {
    var leg = Loci_Trip_TripLeg()
    leg.mode = "train"; leg.durationMins = 194; leg.distanceKm = 274.4
    XCTAssertEqual(MultiCityFormat.leg(leg), "Train · ≈3h14 · 274 km")
    let stop = StopResult(index: 0, cityName: "Lisbon", sessionId: "s", dayNumbers: [1, 2])
    XCTAssertEqual(MultiCityFormat.chip(stop), "Lisbon · 2n")
  }
```

- [ ] **Step 2: Run to verify it fails.**

- [ ] **Step 3: Implement**

`MultiCityFormat.swift`:

```swift
import Foundation
import LociConnectProto

/// Same strings web renders (loci-client multi-city-view.ts), so both apps read alike.
enum MultiCityFormat {
  static func leg(_ leg: Loci_Trip_TripLeg) -> String {
    let mode = ["drive": "Drive", "train": "Train", "bus": "Bus", "flight": "Flight"][leg.mode] ?? "Travel"
    let mins = Int(leg.durationMins)
    let time = mins < 60 ? "\(mins) min" : "\(mins / 60)h" + String(format: "%02d", mins % 60)
    return "\(mode) · ≈\(time) · \(Int(leg.distanceKm.rounded())) km"
  }

  static func chip(_ stop: StopResult) -> String { "\(stop.cityName) · \(stop.dayNumbers.count)n" }
}
```

`StopPicker.swift`: a horizontal `ScrollView` of capsule buttons, including "All days" when `showAll`. Each shows `MultiCityFormat.chip`, a `ProgressView` while `!stop.state.hasResult && stop.error == nil`, and `exclamationmark.triangle` when `error != nil`. It binds to `@Binding var selection: Int?`, where nil means all.

`LegRowView.swift`: an `HStack` with a mode SF Symbol (`car`, `tram`, `bus`, `airplane`), "\(from) → \(to)", the `MultiCityFormat.leg` text in `.secondary`, and a small "Estimate" caption. Reuse `TripEditorView`'s leg row styling (`TripEditorView.swift:120`) if it can be extracted without touching that file's behaviour.

`StopBuilderSheet.swift`: a `NavigationStack` with a `List`:
- a `TextField` + Add row;
- `ForEach` rows, each with the name and a `Stepper("\(n) nights", value: 1...14)`, using `.onMove` and `.onDelete`, capped at 5 with the same dedupe as web;
- a `Toggle("Suggest the best order")`;
- a toolbar "Plan trip" button, disabled below 2 stops, that calls `onPlan(stops, suggest)`.

The composer shows a "Several cities" button that presents it. `onPlan` calls `controller.start(query: query.isEmpty ? "trip" : query, stops: stops, suggestOrder: suggest)`.

`ResultsPage.swift`:
- When `state.isMultiCity`, put `StopPicker` above the content, with `@State private var selectedStop: Int? = nil`.
- The content source is `selectedStop.map { state.stops[$0].state } ?? state`.
- On the itinerary tab with `selectedStop == nil`, render a sectioned list. Each day header reads "Day N · City". After the last day in a city, insert `LegRowView` for the leg with `afterDay == day`. Days come from each stop's `dayGroups`, renumbered through `stop.dayNumbers`.
- A stop with an error shows the existing failure view, scoped to that city.
- `route.outline` sits under the picker, and dropped cities are listed below it.

- [ ] **Step 4: Run.** Unit tests pass. Then run the app on a free simulator and check both paths:
  - Builder: Lisbon (2) and Porto (1) → the picker appears, both cities fill in, and "All days" shows the leg row between days 2 and 3.
  - Composer text "Lisbon for 2 days then Porto for 1": same result.

- [ ] **Step 5: Commit.** `git add loci/loci/Features/Search/UI/MultiCity/ loci/loci/Features/Search/UI/Results/ResultsPage.swift loci/loci/Features/Search/UI/SearchComposer.swift loci/loci.xcodeproj/project.pbxproj loci/lociTests/MultiCityTests.swift && git commit -m "feat(search): multi-city builder, city picker and travel legs"`

(The `project.pbxproj` change is new files only. If the project uses synchronized folders, which Xcode 16 buildable folders do, there is no pbxproj change at all. Check with `git diff --stat`.)

### Task 20: Save and reopen on iOS

**Files:**
- Modify: `loci/loci/Features/Search/Model/SearchEnvelope.swift` (`SearchStore` result snapshot)
- Modify: `loci/loci/Features/Saved/UI/SavedView.swift`
- Test: `loci/lociTests/MultiCityTests.swift`

**Interfaces:**
- Produces: `SearchStore.saveResult(_ state: SearchState)`, which writes the multi-city snapshot as `route` plus one `Loci_Chat_AiCityResponse` per city (serialized protobuf, the same way the single-city snapshot is written today), and `loadResult() -> SearchState?`, which restores `route` and `stops`.

- [ ] **Step 1: Failing test**

```swift
  func testMultiCitySnapshotRoundTrips() throws {
    let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    let store = SearchStore(directory: dir)
    var state = SearchState()
    state.apply(.with { $0.payload = .route(.with { $0.stops = [.with { $0.index = 0; $0.cityName = "A"; $0.dayNumbers = [1] }, .with { $0.index = 1; $0.cityName = "B"; $0.dayNumbers = [2] }]; $0.tripID = "t1" }) })
    state.apply(.with { $0.stopIndex = 1; $0.payload = .itinerary(.with { $0.cityResponse = .with { $0.generalCityData = .with { $0.city = "B" } } }) })
    try store.saveResult(state)
    let back = try XCTUnwrap(store.loadResult())
    XCTAssertEqual(back.stops.map(\.cityName), ["A", "B"])
    XCTAssertEqual(back.stops[1].state.itinerary?.generalCityData.city, "B")
    XCTAssertEqual(back.route?.tripID, "t1")
  }
```

(Use the store's real method names. `grep -n "func " SearchEnvelope.swift` shows how the single-city snapshot is saved; extend those methods rather than adding parallel ones.)

- [ ] **Step 2: Run to verify it fails.**

- [ ] **Step 3: Implement.** The snapshot file gains an optional `route` (serialized `Loci_Chat_RoutePayload`) and `stops: [StopSnapshot]`, where `StopSnapshot` holds `index`, `cityName`, `sessionId`, `dayNumbers` and `result` (a serialized `Loci_Chat_AiCityResponse` built from `stop.state.itinerary`, with hotels, restaurants and activities copied in the same way the single-city snapshot does it). On load, each stop's `state.adopt(result)` rebuilds it.

  `SavedView`'s Itineraries tab lists a multi-city trip as "A + B" when its `TripDraft.cities.count >= 2`, using the parent trip from `TripService.ListTrips`, which the Trips tab already calls. Tapping it opens the trips detail (`TripEditorView`), which already renders per-day cities and legs. Hotels for one city are one tap away from its day header, which opens that city's session results.

- [ ] **Step 4: Run.** Tests pass. Then on the simulator: complete a 2-city search, kill the app and relaunch. The last result should restore with both cities. Saved should show "Lisbon + Porto".

- [ ] **Step 5: Commit and PR**

```bash
git add loci/loci/Features/Search/Model/SearchEnvelope.swift loci/loci/Features/Saved/UI/SavedView.swift loci/lociTests/MultiCityTests.swift
git commit -m "feat(saved): keep and reopen multi-city searches"
git push -u origin feat/multi-city && gh pr create --title "Multi-city trips (iOS)" --fill
```

---

# Part E — Release (all four together)

### Task 21: Ship and verify

- [ ] **Step 1: Merge order.** The proto PR (Task 1) is already merged and tagged `v5.26.0`, and the BSR push is done. Merge the server PR once CI is green. Then open the promote PR in `~/Work/production/platform/infra` by hand, because `INFRA_TOKEN` is unset and CD's promote step silently does nothing:

```bash
cd ~/Work/production/platform/infra && git fetch -q && git worktree add -b promote/loci-<sha> ../infra-promote-loci-<sha> origin/main
# bump apps/loci/api/values-production.yaml image tag (and the rerank/bundle-forge cronjobs if they pin the same tag) to <sha>
git commit -am "Promote Loci API to <sha> (multi-city trips, migration 0101)" && git push -u origin HEAD && gh pr create --fill
```

  First confirm the image tag exists and contains the merge commit. Memory records that the CD tag once did not contain the commit it claimed.
- [ ] **Step 2: Verify the server in prod after ArgoCD syncs.** Read-only checks only; there are no prod DB reads.
  - `KUBECONFIG=~/.kube/maat.yaml kubectl -n horus logs deploy/loci-api | grep -E "goose|0101"` should show migration 101 applied.
  - An unauthenticated probe should prove the contract is live: `buf curl … StreamChat -d '{"message":"x","stops":[{"cityName":"a"},{"cityName":"b"},{"cityName":"c"},{"cityName":"d"},{"cityName":"e"},{"cityName":"f"}]}'` should be rejected by validation (`max_items`) **before** auth.
- [ ] **Step 3:** Merge the web PR, then grep the live bundle for `STREAM_EVENT_TYPE_ROUTE` (or the `route` case) **and** for `api.lociai.fyi`. That's the double-deploy race: if the bundle points at localhost, re-dispatch the Actions deploy.
- [ ] **Step 4:** Merge the iOS PR and let release produce a TestFlight build.
- [ ] **Step 5: End-to-end in prod.**
  - Web: "Lisbon for 2 days then Porto for 1".
  - Pod logs should show `multi-city: city generated` twice.
  - Run the same request again and the logs should show cache hits (served_from memory/db) for both cities.
  - `/saved` → reopen → share.
  - iOS TestFlight: the builder path with 3 cities and "suggest order".
- [ ] **Step 6:** Update memory `loci-multi-city-trips.md` with the PR numbers, the deployed sha and what is still unverified.

---

## Self-review

- **Spec coverage.**
  - Goals 1–6 map to Tasks 9 (one trip, 2–5 cities), 9 + 13 (a day plan per city), 15 (hotels, restaurants and activities per city), 3, 4, 13 and 19 (legs), 8 (suggested order), 5 + 14 + 19 (chat and builder), and 16 + 20 (save, offline, share).
  - Non-goals are respected: nothing books, schedules or gates.
  - Error-handling table: unresolvable city (8), none resolve (8), more than 5 (1 via validation, plus 8 server-side), infeasible route (warnings surfaced in 13), a city failing (6, 9, 12, 17), disconnect (6: the tracker and resume buffer see one START and non-terminal city errors).
  - Release section covered by Task 21.
  - Spec deviation: `RoutePayload` does not embed `MultiCityPlan` (recorded in Global Constraints).
- **Placeholders.** Some steps point at a name to check against the real code: the `generativeAI` import path, the `CityDetail` field names, the tracker fake, `newTestService`/`newTestCache`, the `UnwrappedResults` field and the SearchStore method names. Each says exactly what to grep and what to do with the answer. There is no open-ended TODO.
- **Type consistency.** `StopIndex *int` (Go) ↔ `stop_index` (proto) ↔ `stopIndex?: number` (TS) ↔ `hasStopIndex/stopIndex` (Swift). `StreamRouteData` → `RoutePayload` → `RouteInfo` → `Loci_Chat_RoutePayload`. `runCity`/`runCityFn` are used consistently from Task 9 on. `StopInput` means the same shape on web and iOS.
- **Review Focus.** Each of the five has a test in its owning task: #1 in Task 8, #2 in Tasks 6/9/12/17, #3 in Task 6 (`TestStreamBudget`, plus the per-city timeout in `runSingleCity`), #4 in Task 10, #5 in Tasks 4/5/8.
