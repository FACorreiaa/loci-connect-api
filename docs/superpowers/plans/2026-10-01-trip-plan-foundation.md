# Trip plan foundation (dates, stays, flights) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A trip can hold dates, a stay per city and flights, and both the Connect API and later callers (the chat agent in plan 2) edit them through one `trip.Service`.

**Architecture:** Dates, stays and flights are the trip's "plan". They are written only by dedicated RPCs through a new `PlanRepository.SavePlan`, never by `SaveTrip`. This is the same split sharing already uses (`SetTripVisibility` owns visibility). Clients built before this change send a `TripDraft` without these fields, and `SaveTrip` replaces child rows from whatever it is sent. Each edit is a pure function on `*Trip`, so the rules are unit-tested without a database. `trip.Service` loads the trip, checks the version, applies the edit and saves. Flight links are built on the server by `pkg/flights`, never taken from the client.

**Tech Stack:** Go 1.x, connect-go, protovalidate, pgx v5, goose migrations, Buf (BSR `loci/loci-proto`), testify.

**Spec:** `docs/superpowers/specs/2026-09-30-trip-workflow-agent-actions-design.md` (this branch). This is plan 1 of 5. Plan 2 is the server agent actions, including `RegenerateDays` and `trip_action_proposals`. Plans 3–5 are web, iOS and Telegram.

## Global Constraints

- Dates on the wire are `YYYY-MM-DD` strings. A trip spans at most **30 days** inclusive, the same cap as `pkg/tripspan`.
- A stale `base_version` returns **`FailedPrecondition`** (`ErrVersionConflict` through `toConnectErr`). This follows the codebase, not the spec's "Aborted".
- The server never shows a price it didn't get from the traveller. Flight links are built server-side only. Any `links` or `id` a client sends on a flight is ignored.
- No paid flight APIs and no new Go module dependencies.
- The proto is consumed as a **tagged release**. After bumping it in `go.mod`: `go mod tidy && go mod vendor && make generate`. Run `buf push` from a clean checkout of the tag, because it reads the working tree.
- Another Claude session commits in the same checkouts. Work in worktrees, `git add` explicit paths, never `git add -A`.
- Comments follow the house style: say *why*, in full sentences, as the surrounding code does.

## Review Focus

1. **An older client saves a trip after its plan is set.** Dates, stays and flights must survive `SaveTrip`, and the `SaveTrip` response must include them. Pinned in Task 5.
2. **A trip shared with `share_details` off.** A friend must not see flight notes, price, carrier or flight number, or a stay's booking link. Pinned in Task 9.
3. **A client sends its own flight `links` or `id` to `AddFlight`.** The server discards both and builds its own links. Pinned in Task 8.
4. **Dates set on a trip whose day count differs from the span.** A 3-day trip with 5 days of dates gets days 1–3 dated and no day added or removed. A 31-day span is rejected. Pinned in Task 6.
5. **The same city with different case or spacing.** A stay for `"  lisbon "` replaces the stay for `"Lisbon"` instead of adding a second one. A city not on the trip is rejected. A stale `base_version` saves nothing. Pinned in Tasks 6 and 8.

## File map

**loci-connect-proto**
- Modify `proto/loci/trip/trip.proto`: plan messages, `TripDraft` fields 18–21, six RPCs.

**loci-connect-server**
- Create `pkg/db/migrations/0109_trip_plan.up.sql`. Use the next free number at execution time.
- Create `pkg/flights/flights.go` and `pkg/flights/flights_test.go`: deep-link builder.
- Modify `internal/domain/trip/repository.go`: `Trip` plan fields, plan types, `tripColumns`, `scanTrip`, `loadDays`, `SaveTrip` return path, `insertSnapshot`.
- Create `internal/domain/trip/plan.go` and `internal/domain/trip/plan_test.go`: pure edit rules.
- Create `internal/domain/trip/plan_repository.go` and `internal/domain/trip/plan_integration_test.go`: `SavePlan`.
- Create `internal/domain/trip/service.go` and `internal/domain/trip/service_test.go`: `trip.Service`.
- Create `internal/domain/trip/plan_handler.go` and `internal/domain/trip/plan_handler_test.go`: the RPCs and `WithPlan`.
- Modify `internal/domain/trip/handler.go`: the `plan` field and an `ErrInvalidEdit` case in `toConnectErr`.
- Modify `internal/domain/trip/mappers.go`: plan to proto in `tripToProto`.
- Modify `internal/domain/trip/sharing.go`: redact plan details in `redactForViewer`.
- Modify `cmd/api/dependencies.go:895`: wire `WithPlan`.

---

### Task 1: Proto contracts (loci-connect-proto)

**Files:**
- Modify: `proto/loci/trip/trip.proto`. Add messages after `TripCity` (around line 205), add `TripDraft` fields after `copied_from_trip_id = 17`, add requests after `SetConstraintRequest` (around line 582), and add RPCs after `rpc ReplaceStop` (line 687).

**Interfaces:**
- Produces, as Go types in `github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip`:
  - `TripStay`, `FlightPlace`, `FlightLink`, `TripFlight`, `FlightCabin` (`FlightCabin_FLIGHT_CABIN_ECONOMY` and so on)
  - on `TripDraft`: `StartDate *string`, `EndDate *string`, `Stays []*TripStay`, `Flights []*TripFlight`
  - requests: `SetTripDatesRequest`, `SetStayRequest`, `ClearStayRequest`, `AddFlightRequest`, `RemoveFlightRequest`, `BuildFlightLinksRequest` / `BuildFlightLinksResponse`
  - RPCs on `TripServiceHandler`: `SetTripDates`, `SetStay`, `ClearStay`, `AddFlight`, `RemoveFlight` (each returns `TripDraft`), `BuildFlightLinks`

- [ ] **Step 1: Create a worktree**

```bash
cd ~/Work/production/apps/Loci/loci-connect-proto
git fetch origin && git worktree add -b feat/trip-plan /private/tmp/proto-trip-plan origin/main
cd /private/tmp/proto-trip-plan
```

- [ ] **Step 2: Add the plan messages after `message TripCity { … }`**

```proto
// TripStay is where the traveller sleeps in one city of the trip. Stays are
// keyed by city name, not hung on TripCity, because a single-city trip keeps
// its city on the draft and has no cities entries to hang one on.
message TripStay {
  string city_name = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 200
  }];
  string poi_id = 2 [(buf.validate.field).string.max_len = 100];
  string name = 3 [(buf.validate.field).string = {
    min_len: 1
    max_len: 300
  }];
  string star_rating = 4 [(buf.validate.field).string.max_len = 10];
  optional string check_in = 5 [(buf.validate.field).string.pattern = "^[0-9]{4}-[0-9]{2}-[0-9]{2}$"];
  optional string check_out = 6 [(buf.validate.field).string.pattern = "^[0-9]{4}-[0-9]{2}-[0-9]{2}$"];
  // Shown to friends only when the trip shares details, like a stop's link.
  optional string booking_url = 7 [(buf.validate.field).string = {
    max_len: 2000
    prefix: "https://"
  }];
}

enum FlightCabin {
  FLIGHT_CABIN_UNSPECIFIED = 0;
  FLIGHT_CABIN_ECONOMY = 1;
  FLIGHT_CABIN_PREMIUM_ECONOMY = 2;
  FLIGHT_CABIN_BUSINESS = 3;
  FLIGHT_CABIN_FIRST = 4;
}

// FlightPlace is one end of a flight. iata is optional: Google Flights takes a
// city name, and Skyscanner links are only built when both ends have one.
message FlightPlace {
  string name = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 200
  }];
  optional string iata = 2 [(buf.validate.field).string.pattern = "^[A-Z]{3}$"];
}

// FlightLink is a prefilled search on a site that sells the ticket. The server
// builds these; any a client sends are ignored.
message FlightLink {
  string provider = 1;
  string label = 2;
  string url = 3;
}

// TripFlight is a flight the traveller chose. Loci never quotes a fare:
// price_text is whatever the traveller typed, shown back to them as-is.
message TripFlight {
  // Assigned by the server on AddFlight.
  string id = 1 [(buf.validate.field).string.max_len = 100];
  FlightPlace origin = 2 [(buf.validate.field).required = true];
  FlightPlace destination = 3 [(buf.validate.field).required = true];
  string depart_date = 4 [(buf.validate.field).string.pattern = "^[0-9]{4}-[0-9]{2}-[0-9]{2}$"];
  optional string return_date = 5 [(buf.validate.field).string.pattern = "^[0-9]{4}-[0-9]{2}-[0-9]{2}$"];
  int32 passengers = 6 [(buf.validate.field).int32 = {
    gte: 1
    lte: 9
  }];
  FlightCabin cabin = 7;
  // Server-built; ignored on the way in.
  repeated FlightLink links = 8;
  optional string carrier = 9 [(buf.validate.field).string.max_len = 100];
  optional string flight_no = 10 [(buf.validate.field).string.max_len = 20];
  optional string price_text = 11 [(buf.validate.field).string.max_len = 50];
  optional string notes = 12 [(buf.validate.field).string.max_len = 1000];

  option (buf.validate.message).cel = {
    id: "trip_flight.return_after_depart"
    message: "return_date must not be before depart_date"
    expression: "!has(this.return_date) || this.return_date >= this.depart_date"
  };
}
```

- [ ] **Step 3: Add the `TripDraft` fields after `optional string copied_from_trip_id = 17 …;`**

```proto
  // The trip's plan: dates, where it sleeps and its flights. SaveTrip neither
  // reads nor writes these; SetTripDates, SetStay/ClearStay and
  // AddFlight/RemoveFlight own them, so a client that predates them cannot
  // wipe them by saving.
  optional string start_date = 18 [(buf.validate.field).string.pattern = "^[0-9]{4}-[0-9]{2}-[0-9]{2}$"];
  optional string end_date = 19 [(buf.validate.field).string.pattern = "^[0-9]{4}-[0-9]{2}-[0-9]{2}$"];
  repeated TripStay stays = 20;
  repeated TripFlight flights = 21;
```

- [ ] **Step 4: Add the requests after `message SetConstraintRequest { … }`**

```proto
// SetTripDatesRequest dates the trip and stamps each day: day N falls on
// start_date + N - 1. Days are never added or removed; when the day count and
// the span differ, the client offers to re-plan.
message SetTripDatesRequest {
  string trip_id = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 100
  }];
  string start_date = 2 [(buf.validate.field).string.pattern = "^[0-9]{4}-[0-9]{2}-[0-9]{2}$"];
  string end_date = 3 [(buf.validate.field).string.pattern = "^[0-9]{4}-[0-9]{2}-[0-9]{2}$"];
  int64 base_version = 4 [(buf.validate.field).int64 = {gte: 0}];

  option (buf.validate.message).cel = {
    id: "set_trip_dates.end_after_start"
    message: "end_date must not be before start_date"
    expression: "this.end_date >= this.start_date"
  };
}

message SetStayRequest {
  string trip_id = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 100
  }];
  TripStay stay = 2 [(buf.validate.field).required = true];
  int64 base_version = 3 [(buf.validate.field).int64 = {gte: 0}];
}

message ClearStayRequest {
  string trip_id = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 100
  }];
  string city_name = 2 [(buf.validate.field).string = {
    min_len: 1
    max_len: 200
  }];
  int64 base_version = 3 [(buf.validate.field).int64 = {gte: 0}];
}

message AddFlightRequest {
  string trip_id = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 100
  }];
  TripFlight flight = 2 [(buf.validate.field).required = true];
  int64 base_version = 3 [(buf.validate.field).int64 = {gte: 0}];
}

message RemoveFlightRequest {
  string trip_id = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 100
  }];
  string flight_id = 2 [(buf.validate.field).string = {
    min_len: 1
    max_len: 100
  }];
  int64 base_version = 3 [(buf.validate.field).int64 = {gte: 0}];
}

// BuildFlightLinksRequest is the manual flight form's search: no trip, no
// write, just the links the server would attach on AddFlight.
message BuildFlightLinksRequest {
  FlightPlace origin = 1 [(buf.validate.field).required = true];
  FlightPlace destination = 2 [(buf.validate.field).required = true];
  string depart_date = 3 [(buf.validate.field).string.pattern = "^[0-9]{4}-[0-9]{2}-[0-9]{2}$"];
  optional string return_date = 4 [(buf.validate.field).string.pattern = "^[0-9]{4}-[0-9]{2}-[0-9]{2}$"];
  int32 passengers = 5 [(buf.validate.field).int32 = {
    gte: 1
    lte: 9
  }];
  FlightCabin cabin = 6;

  option (buf.validate.message).cel = {
    id: "build_flight_links.return_after_depart"
    message: "return_date must not be before depart_date"
    expression: "!has(this.return_date) || this.return_date >= this.depart_date"
  };
}

message BuildFlightLinksResponse {
  repeated FlightLink links = 1;
}
```

- [ ] **Step 5: Add the RPCs after `rpc ReplaceStop(ReplaceStopRequest) returns (TripDraft);`**

```proto
  // The trip's plan. Each takes base_version and answers FailedPrecondition
  // when it is stale, like the stop edits above.
  rpc SetTripDates(SetTripDatesRequest) returns (TripDraft);
  rpc SetStay(SetStayRequest) returns (TripDraft);
  rpc ClearStay(ClearStayRequest) returns (TripDraft);
  rpc AddFlight(AddFlightRequest) returns (TripDraft);
  rpc RemoveFlight(RemoveFlightRequest) returns (TripDraft);
  rpc BuildFlightLinks(BuildFlightLinksRequest) returns (BuildFlightLinksResponse);
```

- [ ] **Step 6: Lint, check for breaking changes, generate**

Run: `buf lint && buf breaking --against '.git#branch=origin/main' && buf generate && make bruno`
Expected: no lint errors and no breaking changes (every change is additive). New code appears under `gen/go/loci/trip/`, `gen/swift/…` and the TS output. Check the header of a generated file: the `protoc-gen-go` version must match the one `buf.gen.yaml` pins. If it doesn't, the file came from a local toolchain; leave that churn out of the commit.

- [ ] **Step 7: Commit, open the PR**

```bash
git status --short   # make bruno may write outside gen/; stage only what belongs to this repo
git add proto/loci/trip/trip.proto gen/
git commit -m "feat(trip): dates, stays and flights on a trip, and flight links"
git push -u origin feat/trip-plan && gh pr create --fill
```

- [ ] **Step 8: After the merge, release from a clean checkout of the tag**

```bash
git fetch --tags origin && git tag --sort=-creatordate | head -3   # pick the next free minor; v5.32.0 if v5.31.1 is still newest
git switch --detach origin/main && git status --porcelain            # must print nothing
make release VERSION=v5.32.0
git show v5.32.0:proto/loci/trip/trip.proto | grep -c "rpc SetTripDates"   # expect 1
```

---

### Task 2: Server proto bump

**Files:**
- Modify: `go.mod`, `go.sum`, `vendor/`, `gen/` (via `make generate`).

**Interfaces:**
- Consumes: the Task 1 tag.
- Produces: the server compiles with the new `TripServiceHandler` methods, served as `Unimplemented` through the embedded `tripconnect.UnimplementedTripServiceHandler`.

- [ ] **Step 1: Create a worktree**

```bash
cd ~/Work/production/apps/Loci/loci-connect-server
git fetch origin && git worktree add -b feat/trip-plan /private/tmp/api-trip-plan origin/main && cd /private/tmp/api-trip-plan
```

- [ ] **Step 2: Bump and regenerate**

Run: `go get github.com/FACorreiaa/loci-connect-proto/v5@v5.32.0 && go mod tidy && go mod vendor && make generate && go build ./...`
Expected: the build succeeds and `grep -c SetTripDates gen/go/loci/trip/tripconnect/*.go` is non-zero.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum vendor gen
git commit -m "chore(deps): loci-connect-proto v5.32.0"
```

---

### Task 3: Migration

**Files:**
- Create: `pkg/db/migrations/0109_trip_plan.up.sql`. Run `ls pkg/db/migrations | tail -2` first and take the next free number.

**Interfaces:**
- Produces: the columns `trips.start_date`/`end_date` (DATE NULL), and the tables `trip_stays` and `trip_flights`, as used by Tasks 5 and 7.

- [ ] **Step 1: Write the migration**

```sql
-- +goose Up
-- +goose StatementBegin
-- A trip's plan: its dates, where it sleeps and its flights. Day dates stay on
-- trip_days.date (the calendar and ICS feed read them there); start_date and
-- end_date are what the traveller chose, which a 3-day plan inside a 5-day
-- trip needs to keep.
ALTER TABLE trips
    ADD COLUMN IF NOT EXISTS start_date DATE NULL,
    ADD COLUMN IF NOT EXISTS end_date   DATE NULL;
ALTER TABLE trips
    ADD CONSTRAINT trips_dates_ordered
    CHECK (start_date IS NULL OR end_date IS NULL OR end_date >= start_date);

-- One stay per city, matched case-insensitively: "Lisbon" and "lisbon " are
-- the same city to a traveller. poi_id is empty when the hotel came from a
-- generated list with no stored POI.
CREATE TABLE IF NOT EXISTS trip_stays (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trip_id     UUID NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    city_name   TEXT NOT NULL,
    poi_id      TEXT NOT NULL DEFAULT '',
    name        TEXT NOT NULL,
    star_rating TEXT NOT NULL DEFAULT '',
    check_in    DATE NULL,
    check_out   DATE NULL,
    booking_url TEXT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_trip_stays_trip_city
    ON trip_stays (trip_id, lower(btrim(city_name)));

-- Flights the traveller chose. links is server-built JSON
-- ([{provider,label,url}]); price_text is whatever the traveller typed and is
-- never a quoted fare. ids are assigned by the server and kept across saves so
-- RemoveFlight can name one.
CREATE TABLE IF NOT EXISTS trip_flights (
    id               UUID PRIMARY KEY,
    trip_id          UUID NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    origin_name      TEXT NOT NULL,
    origin_iata      TEXT NOT NULL DEFAULT '',
    destination_name TEXT NOT NULL,
    destination_iata TEXT NOT NULL DEFAULT '',
    depart_date      DATE NOT NULL,
    return_date      DATE NULL,
    passengers       INT  NOT NULL DEFAULT 1,
    cabin            INT  NOT NULL DEFAULT 0,
    links            JSONB NOT NULL DEFAULT '[]',
    carrier          TEXT NULL,
    flight_no        TEXT NULL,
    price_text       TEXT NULL,
    notes            TEXT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_trip_flights_trip ON trip_flights (trip_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS trip_flights;
DROP TABLE IF EXISTS trip_stays;
ALTER TABLE trips DROP CONSTRAINT IF EXISTS trips_dates_ordered;
ALTER TABLE trips
    DROP COLUMN IF EXISTS end_date,
    DROP COLUMN IF EXISTS start_date;
-- +goose StatementEnd
```

- [ ] **Step 2: Check it**

Run: `git add pkg/db/migrations/0109_trip_plan.up.sql && .githooks/pre-commit 2>&1 | grep check-migrations` (or the hook path the repo uses; see `make hooks`).
Expected: `check-migrations: N migrations, no duplicate versions.`

- [ ] **Step 3: Commit**

```bash
git commit -m "feat(trip): migration for trip dates, stays and flights"
```

---

### Task 4: `pkg/flights` deep links

**Files:**
- Create: `pkg/flights/flights.go`
- Test: `pkg/flights/flights_test.go`

**Interfaces:**
- Produces:
  - `type Cabin int` with `CabinUnspecified, CabinEconomy, CabinPremiumEconomy, CabinBusiness, CabinFirst`. These are numerically equal to the proto `FlightCabin`.
  - `type Place struct{ Name, IATA string }`
  - `type Query struct{ Origin, Destination Place; Depart time.Time; Return *time.Time; Passengers int; Cabin Cabin }`
  - `type Link struct{ Provider, Label, URL string }`, with json tags `provider`, `label` and `url`
  - `type Provider interface{ Links(Query) []Link }`
  - `type DeepLinks struct{}`, which implements `Provider`
  - `func ValidIATA(s string) bool`

- [ ] **Step 1: Write the failing test**

```go
package flights

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func date(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestDeepLinks(t *testing.T) {
	ret := date("2026-11-17")
	tests := []struct {
		name string
		q    Query
		want []Link
	}{
		{
			name: "round trip with IATA on both ends gets Google and Skyscanner",
			q: Query{
				Origin: Place{Name: "New York", IATA: "JFK"}, Destination: Place{Name: "Lisbon", IATA: "LIS"},
				Depart: date("2026-11-10"), Return: &ret, Passengers: 2, Cabin: CabinEconomy,
			},
			want: []Link{
				{Provider: "google_flights", Label: "Google Flights", URL: "https://www.google.com/travel/flights?q=Flights+to+LIS+from+JFK+on+2026-11-10+through+2026-11-17+for+2+adults+economy"},
				{Provider: "skyscanner", Label: "Skyscanner", URL: "https://www.skyscanner.net/transport/flights/jfk/lis/261110/261117/?adultsv2=2&cabinclass=economy&rtn=1"},
			},
		},
		{
			name: "one way with names only gets Google alone",
			q: Query{
				Origin: Place{Name: "Porto"}, Destination: Place{Name: "Warsaw"},
				Depart: date("2026-12-01"), Passengers: 1,
			},
			want: []Link{
				{Provider: "google_flights", Label: "Google Flights", URL: "https://www.google.com/travel/flights?q=Flights+to+Warsaw+from+Porto+on+2026-12-01+one+way"},
			},
		},
		{
			name: "a malformed IATA is not used anywhere",
			q: Query{
				Origin: Place{Name: "Lisbon", IATA: "lis"}, Destination: Place{Name: "Rome", IATA: "FCO"},
				Depart: date("2026-12-01"), Passengers: 0, Cabin: CabinBusiness,
			},
			want: []Link{
				{Provider: "google_flights", Label: "Google Flights", URL: "https://www.google.com/travel/flights?q=Flights+to+FCO+from+Lisbon+on+2026-12-01+one+way+business+class"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, DeepLinks{}.Links(tt.q))
		})
	}
}

func TestSkyscannerOneWayAndCabins(t *testing.T) {
	links := DeepLinks{}.Links(Query{
		Origin: Place{Name: "Lisbon", IATA: "LIS"}, Destination: Place{Name: "Rome", IATA: "FCO"},
		Depart: date("2026-12-01"), Passengers: 1, Cabin: CabinPremiumEconomy,
	})
	require.Len(t, links, 2)
	require.Equal(t, "https://www.skyscanner.net/transport/flights/lis/fco/261201/?adultsv2=1&cabinclass=premiumeconomy&rtn=0", links[1].URL)
}

func TestValidIATA(t *testing.T) {
	require.True(t, ValidIATA("LIS"))
	for _, s := range []string{"", "lis", "LISB", "L1S", " LIS"} {
		require.False(t, ValidIATA(s), s)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./pkg/flights/`
Expected: FAIL to compile with `undefined: Query` / `undefined: DeepLinks`.

- [ ] **Step 3: Implement**

```go
// Package flights builds links to flight search sites. It never quotes a
// price: v1 hands the traveller a prefilled search on a site that sells the
// ticket. The research behind this (2026-09-30) found no free live-fare API an
// app with no users can get: Amadeus Self-Service shut down on 2026-07-17 and
// Kiwi wants 50k monthly users. Provider is the seam a paid source would use.
package flights

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Cabin matches loci.trip.FlightCabin value for value.
type Cabin int

const (
	CabinUnspecified Cabin = iota
	CabinEconomy
	CabinPremiumEconomy
	CabinBusiness
	CabinFirst
)

// Place is one end of a flight. IATA is optional.
type Place struct {
	Name string
	IATA string
}

// Query is one flight search.
type Query struct {
	Origin      Place
	Destination Place
	Depart      time.Time
	Return      *time.Time
	Passengers  int
	Cabin       Cabin
}

// Link is a prefilled search on one site.
type Link struct {
	Provider string `json:"provider"`
	Label    string `json:"label"`
	URL      string `json:"url"`
}

// Provider turns a query into links.
type Provider interface {
	Links(Query) []Link
}

var iataRE = regexp.MustCompile(`^[A-Z]{3}$`)

// ValidIATA reports whether s looks like an IATA airport or city code. It
// checks shape only; an LLM-supplied code that is well formed but wrong still
// yields a search the traveller can correct on the site.
func ValidIATA(s string) bool { return iataRE.MatchString(s) }

// DeepLinks builds Google Flights links always, and Skyscanner links when both
// ends carry a valid IATA code (Skyscanner's URL takes codes, not names).
type DeepLinks struct{}

func (DeepLinks) Links(q Query) []Link {
	links := []Link{googleFlights(q)}
	if l, ok := skyscanner(q); ok {
		links = append(links, l)
	}
	return links
}

// googleFlights uses the natural-language q= form. The structured tfs=
// parameter is an undocumented encoded blob; q= takes names or codes.
func googleFlights(q Query) Link {
	var b strings.Builder
	fmt.Fprintf(&b, "Flights to %s from %s on %s", placeTerm(q.Destination), placeTerm(q.Origin), q.Depart.Format(time.DateOnly))
	if q.Return != nil {
		fmt.Fprintf(&b, " through %s", q.Return.Format(time.DateOnly))
	} else {
		b.WriteString(" one way")
	}
	if q.Passengers > 1 {
		fmt.Fprintf(&b, " for %d adults", q.Passengers)
	}
	if c := googleCabin(q.Cabin); c != "" {
		b.WriteString(" " + c)
	}
	v := url.Values{"q": {b.String()}}
	return Link{Provider: "google_flights", Label: "Google Flights", URL: "https://www.google.com/travel/flights?" + v.Encode()}
}

// placeTerm prefers the code: "Porto" alone can land on Porto Alegre.
func placeTerm(p Place) string {
	if ValidIATA(p.IATA) {
		return p.IATA
	}
	return p.Name
}

func googleCabin(c Cabin) string {
	switch c {
	case CabinEconomy:
		return "economy"
	case CabinPremiumEconomy:
		return "premium economy"
	case CabinBusiness:
		return "business class"
	case CabinFirst:
		return "first class"
	default:
		return ""
	}
}

func skyscanner(q Query) (Link, bool) {
	if !ValidIATA(q.Origin.IATA) || !ValidIATA(q.Destination.IATA) {
		return Link{}, false
	}
	u := fmt.Sprintf("https://www.skyscanner.net/transport/flights/%s/%s/%s/",
		strings.ToLower(q.Origin.IATA), strings.ToLower(q.Destination.IATA), q.Depart.Format("060102"))
	rtn := "0"
	if q.Return != nil {
		u += q.Return.Format("060102") + "/"
		rtn = "1"
	}
	v := url.Values{
		"adultsv2":   {strconv.Itoa(max(q.Passengers, 1))},
		"cabinclass": {skyscannerCabin(q.Cabin)},
		"rtn":        {rtn},
	}
	return Link{Provider: "skyscanner", Label: "Skyscanner", URL: u + "?" + v.Encode()}, true
}

func skyscannerCabin(c Cabin) string {
	switch c {
	case CabinPremiumEconomy:
		return "premiumeconomy"
	case CabinBusiness:
		return "business"
	case CabinFirst:
		return "first"
	default:
		return "economy"
	}
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `go test ./pkg/flights/`
Expected: PASS.

- [ ] **Step 5: Check the URL formats by hand**

Open each `want` URL from the tests in a browser. Google must open a prefilled search; Skyscanner must open the right route and dates. These formats were never checked against a live source (the research ran out of credits). If one is wrong, fix the builder and its test together.

- [ ] **Step 6: Commit**

```bash
git add pkg/flights/flights.go pkg/flights/flights_test.go
git commit -m "feat(flights): Google Flights and Skyscanner deep links"
```

---

### Task 5: Trip aggregate carries its plan (read side, and SaveTrip leaves it alone)

**Files:**
- Modify: `internal/domain/trip/repository.go`. Touch the `Trip` struct (around line 104), `tripColumns`/`scanTrip` (around line 556), `loadDays` (around line 481), and `SaveTrip` (UPDATE at around line 252, snapshot at around line 305).
- Test: `internal/domain/trip/plan_integration_test.go` (`//go:build integration`; it uses `testTripDB` and `newTripUser` from `multicity_integration_test.go`).

**Interfaces:**
- Consumes: `flights.Place`, `flights.Cabin`, `flights.Link` (Task 4), and the Task 3 schema.
- Produces, in package `trip`:
  - `type TripStay struct{ ID uuid.UUID; CityName, POIID, Name, StarRating string; CheckIn, CheckOut *time.Time; BookingURL *string }`
  - `type TripFlight struct{ ID uuid.UUID; Origin, Destination flights.Place; DepartDate time.Time; ReturnDate *time.Time; Passengers int32; Cabin flights.Cabin; Links []flights.Link; Carrier, FlightNo, PriceText, Notes *string }`
  - new `Trip` fields: `StartDate, EndDate *time.Time; Stays []TripStay; Flights []TripFlight`
  - `func (r *repository) loadPlan(ctx context.Context, t *Trip) error`
  - `func insertSnapshot(ctx context.Context, tx pgx.Tx, t *Trip) error`

- [ ] **Step 1: Write the failing integration test**

This test writes the plan rows directly with SQL, because `SavePlan` comes in Task 7.

```go
//go:build integration

package trip

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// A client built before trip plans saves the whole TripDraft it holds, which
// has no dates, stays or flights. SaveTrip must leave them in place, and its
// response must still carry them, or the editor shows them gone until reload.
func TestRepository_SaveTripKeepsThePlan(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testTripDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
	userID := newTripUser(t, "plan-keep-"+uuid.NewString()+"@loci.test")

	first, err := repo.SaveTrip(ctx, &Trip{
		UserID: userID, CityName: "Lisbon", Title: "Lisbon",
		Days: []TripDay{{DayNumber: 1, CityName: "Lisbon"}},
	}, 0)
	require.NoError(t, err)

	_, err = testTripDB.Exec(ctx, `UPDATE trips SET start_date = '2026-11-12', end_date = '2026-11-14' WHERE id = $1`, first.ID)
	require.NoError(t, err)
	_, err = testTripDB.Exec(ctx, `INSERT INTO trip_stays (trip_id, city_name, name, star_rating) VALUES ($1, 'Lisbon', 'Hotel Avenida', '4')`, first.ID)
	require.NoError(t, err)
	_, err = testTripDB.Exec(ctx, `
		INSERT INTO trip_flights (id, trip_id, origin_name, destination_name, destination_iata, depart_date, links)
		VALUES ($1, $2, 'New York', 'Lisbon', 'LIS', '2026-11-12', '[{"provider":"google_flights","label":"Google Flights","url":"https://www.google.com/travel/flights?q=x"}]')`,
		uuid.New(), first.ID)
	require.NoError(t, err)

	// An old client's save: the draft it holds has no plan at all.
	old := *first
	old.StartDate, old.EndDate, old.Stays, old.Flights = nil, nil, nil, nil
	old.Title = "Lisbon long weekend"
	saved, err := repo.SaveTrip(ctx, &old, first.Version)
	require.NoError(t, err)

	for _, got := range []*Trip{saved, mustGet(t, repo, first.ID, userID)} {
		require.NotNil(t, got.StartDate)
		require.Equal(t, "2026-11-12", got.StartDate.Format(time.DateOnly))
		require.Equal(t, "2026-11-14", got.EndDate.Format(time.DateOnly))
		require.Len(t, got.Stays, 1)
		require.Equal(t, "Hotel Avenida", got.Stays[0].Name)
		require.Len(t, got.Flights, 1)
		require.Equal(t, "LIS", got.Flights[0].Destination.IATA)
		require.Len(t, got.Flights[0].Links, 1)
	}
}

func mustGet(t *testing.T, repo Repository, id, userID uuid.UUID) *Trip {
	t.Helper()
	got, err := repo.GetTrip(context.Background(), id, userID)
	require.NoError(t, err)
	return got
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test -tags=integration -p 1 -count=1 ./internal/domain/trip/ -run TestRepository_SaveTripKeepsThePlan`
Expected: FAIL to compile with `old.StartDate undefined`.

- [ ] **Step 3: Add the types and fields**

In `repository.go`, add `"github.com/FACorreiaa/loci-connect-api/pkg/flights"` to the imports. After `type TripCity struct { … }`, add:

```go
// TripStay is where the traveller sleeps in one city. Keyed by city name, not
// by TripCity, because a single-city trip has no Cities entries.
type TripStay struct {
	ID         uuid.UUID
	CityName   string
	POIID      string
	Name       string
	StarRating string
	CheckIn    *time.Time
	CheckOut   *time.Time
	BookingURL *string
}

// TripFlight is a flight the traveller chose. Links are server-built; the
// pointer fields are whatever the traveller typed, never a quoted fare.
type TripFlight struct {
	ID          uuid.UUID
	Origin      flights.Place
	Destination flights.Place
	DepartDate  time.Time
	ReturnDate  *time.Time
	Passengers  int32
	Cabin       flights.Cabin
	Links       []flights.Link
	Carrier     *string
	FlightNo    *string
	PriceText   *string
	Notes       *string
}
```

In `type Trip struct`, after `Cities []TripCity`:

```go
	// The plan: dates, stays and flights. SaveTrip never writes these (see
	// PlanRepository); it reads them back so its response is complete.
	StartDate *time.Time
	EndDate   *time.Time
	Stays     []TripStay
	Flights   []TripFlight
```

- [ ] **Step 4: Read dates with every trip**

Append `, start_date, end_date` to `tripColumns`. In `scanTrip`, add `&t.StartDate, &t.EndDate` after `&t.CopiedFromTripID`. Then run `git grep -n "tripColumns\|scanTrip(" internal/` and check every caller still selects exactly `tripColumns`, with no hand-written column list that `scanTrip` reads.

- [ ] **Step 5: Load stays and flights**

Add to `repository.go`:

```go
// loadPlan populates t.Stays and t.Flights. Dates come with the trip row.
func (r *repository) loadPlan(ctx context.Context, t *Trip) error {
	rows, err := r.db.Query(ctx, `
		SELECT id, city_name, poi_id, name, star_rating, check_in, check_out, booking_url
		FROM trip_stays WHERE trip_id = $1 ORDER BY check_in NULLS LAST, city_name`, t.ID)
	if err != nil {
		return fmt.Errorf("load stays: %w", err)
	}
	t.Stays = nil
	for rows.Next() {
		var s TripStay
		if err := rows.Scan(&s.ID, &s.CityName, &s.POIID, &s.Name, &s.StarRating, &s.CheckIn, &s.CheckOut, &s.BookingURL); err != nil {
			rows.Close()
			return fmt.Errorf("scan stay: %w", err)
		}
		t.Stays = append(t.Stays, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("load stays: %w", err)
	}

	rows, err = r.db.Query(ctx, `
		SELECT id, origin_name, origin_iata, destination_name, destination_iata, depart_date, return_date,
		       passengers, cabin, links, carrier, flight_no, price_text, notes
		FROM trip_flights WHERE trip_id = $1 ORDER BY depart_date, created_at`, t.ID)
	if err != nil {
		return fmt.Errorf("load flights: %w", err)
	}
	defer rows.Close()
	t.Flights = nil
	for rows.Next() {
		var (
			f         TripFlight
			cabin     int32
			linksJSON []byte
		)
		if err := rows.Scan(&f.ID, &f.Origin.Name, &f.Origin.IATA, &f.Destination.Name, &f.Destination.IATA,
			&f.DepartDate, &f.ReturnDate, &f.Passengers, &cabin, &linksJSON,
			&f.Carrier, &f.FlightNo, &f.PriceText, &f.Notes); err != nil {
			return fmt.Errorf("scan flight: %w", err)
		}
		f.Cabin = flights.Cabin(cabin)
		if err := json.Unmarshal(linksJSON, &f.Links); err != nil {
			return fmt.Errorf("unmarshal flight links: %w", err)
		}
		t.Flights = append(t.Flights, f)
	}
	return rows.Err()
}
```

In `loadDays`, directly after the existing `if err := r.loadLegs(ctx, t); err != nil { … }` block, add:

```go
	if err := r.loadPlan(ctx, t); err != nil {
		return err
	}
```

- [ ] **Step 6: Make SaveTrip return the stored plan**

In the existing-trip `UPDATE trips … RETURNING` of `SaveTrip`, append `start_date, end_date` to the `RETURNING` list and `&t.StartDate, &t.EndDate` to its `Scan`. Then replace the snapshot block with a call to a new helper, and load the plan after the commit:

```go
	if err := insertSnapshot(ctx, tx, t); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	// The client's draft carries no plan; what is stored is the truth.
	if err := r.loadPlan(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

// insertSnapshot appends the immutable per-version snapshot every save writes.
func insertSnapshot(ctx context.Context, tx pgx.Tx, t *Trip) error {
	snapshotJSON, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO trip_snapshots (trip_id, version, data) VALUES ($1, $2, $3)`,
		t.ID, t.Version, snapshotJSON); err != nil {
		return fmt.Errorf("insert snapshot: %w", err)
	}
	return nil
}
```

`t.Version` is already `newVersion` at that point, because `SaveTrip` sets `t.Version = newVersion` before replacing the days.

- [ ] **Step 7: Run the tests**

Run: `go test -tags=integration -p 1 -count=1 ./internal/domain/trip/ && go test ./internal/domain/... `
Expected: PASS, including `TestRepository_SaveTripKeepsDayAndStopIDs`.

- [ ] **Step 8: Commit**

```bash
git add internal/domain/trip/repository.go internal/domain/trip/plan_integration_test.go
git commit -m "feat(trip): read dates, stays and flights with a trip; SaveTrip leaves them alone"
```

---

### Task 6: Plan edit rules (pure)

**Files:**
- Create: `internal/domain/trip/plan.go`
- Test: `internal/domain/trip/plan_test.go`

**Interfaces:**
- Consumes: the `Trip`, `TripStay` and `TripFlight` types (Task 5), and `flights.ValidIATA` (Task 4).
- Produces:
  - `var ErrInvalidEdit = errors.New("invalid trip edit")`
  - `const MaxTripSpanDays = 30`
  - `func applyDates(t *Trip, start, end time.Time) error`
  - `func upsertStay(t *Trip, s TripStay) error`
  - `func removeStay(t *Trip, city string) error`
  - `func appendFlight(t *Trip, f TripFlight) (TripFlight, error)`: assigns `ID`, and defaults `Passengers` to 1
  - `func removeFlight(t *Trip, id uuid.UUID) error`
  - `func (t *Trip) HasCity(name string) bool`

  Every validation error wraps `ErrInvalidEdit`.

- [ ] **Step 1: Write the failing tests**

```go
package trip

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
)

func day(s string) time.Time {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return d
}

func threeDayLisbon() *Trip {
	return &Trip{CityName: "Lisbon", Days: []TripDay{{DayNumber: 1}, {DayNumber: 2}, {DayNumber: 3}}}
}

func TestApplyDates_StampsDaysAndNeverResizes(t *testing.T) {
	tr := threeDayLisbon()
	require.NoError(t, applyDates(tr, day("2026-11-12"), day("2026-11-16")))
	require.Equal(t, "2026-11-12", tr.StartDate.Format(time.DateOnly))
	require.Equal(t, "2026-11-16", tr.EndDate.Format(time.DateOnly))
	require.Len(t, tr.Days, 3, "dates never add or drop days")
	require.Equal(t, "2026-11-12", tr.Days[0].Date.Format(time.DateOnly))
	require.Equal(t, "2026-11-14", tr.Days[2].Date.Format(time.DateOnly))
}

func TestApplyDates_Rejects(t *testing.T) {
	require.ErrorIs(t, applyDates(threeDayLisbon(), day("2026-11-16"), day("2026-11-12")), ErrInvalidEdit)
	// 2026-11-01 .. 2026-12-01 is 31 days inclusive.
	require.ErrorIs(t, applyDates(threeDayLisbon(), day("2026-11-01"), day("2026-12-01")), ErrInvalidEdit)
	require.NoError(t, applyDates(threeDayLisbon(), day("2026-11-01"), day("2026-11-30")), "30 days is allowed")
}

func TestUpsertStay_MatchesCityLooselyAndReplaces(t *testing.T) {
	tr := threeDayLisbon()
	require.NoError(t, upsertStay(tr, TripStay{CityName: "Lisbon", Name: "Hotel Avenida", StarRating: "4"}))
	require.NoError(t, upsertStay(tr, TripStay{CityName: "  lisbon ", Name: "Pestana Palace", StarRating: "5"}))
	require.Len(t, tr.Stays, 1, "same city, different spelling, replaces")
	require.Equal(t, "Pestana Palace", tr.Stays[0].Name)
	require.Equal(t, "lisbon", tr.Stays[0].CityName, "stored trimmed")
}

func TestUpsertStay_Rejects(t *testing.T) {
	tr := threeDayLisbon()
	require.ErrorIs(t, upsertStay(tr, TripStay{CityName: "Porto", Name: "X"}), ErrInvalidEdit, "not a city on the trip")
	require.ErrorIs(t, upsertStay(tr, TripStay{CityName: "Lisbon", Name: "  "}), ErrInvalidEdit, "needs a name")
	in, out := day("2026-11-14"), day("2026-11-12")
	require.ErrorIs(t, upsertStay(tr, TripStay{CityName: "Lisbon", Name: "X", CheckIn: &in, CheckOut: &out}), ErrInvalidEdit)
}

func TestHasCity_MultiCity(t *testing.T) {
	tr := &Trip{CityName: "Lisbon", Cities: []TripCity{{CityName: "Lisbon"}, {CityName: "Porto"}}}
	require.True(t, tr.HasCity("PORTO"))
	require.False(t, tr.HasCity("Faro"))
}

func TestRemoveStay(t *testing.T) {
	tr := threeDayLisbon()
	require.NoError(t, upsertStay(tr, TripStay{CityName: "Lisbon", Name: "Hotel Avenida"}))
	require.NoError(t, removeStay(tr, "LISBON"))
	require.Empty(t, tr.Stays)
	require.ErrorIs(t, removeStay(tr, "Lisbon"), ErrInvalidEdit, "nothing to remove")
}

func TestAppendAndRemoveFlight(t *testing.T) {
	tr := threeDayLisbon()
	clientID := uuid.New()
	f, err := appendFlight(tr, TripFlight{
		ID:     clientID,
		Origin: flights.Place{Name: "New York", IATA: "JFK"}, Destination: flights.Place{Name: "Lisbon", IATA: "LIS"},
		DepartDate: day("2026-11-12"),
	})
	require.NoError(t, err)
	require.NotEqual(t, clientID, f.ID, "the server assigns flight ids")
	require.EqualValues(t, 1, f.Passengers, "defaults to one traveller")
	require.Len(t, tr.Flights, 1)

	require.ErrorIs(t, removeFlight(tr, uuid.New()), ErrInvalidEdit)
	require.NoError(t, removeFlight(tr, f.ID))
	require.Empty(t, tr.Flights)
}

func TestAppendFlight_Rejects(t *testing.T) {
	back := day("2026-11-10")
	cases := map[string]TripFlight{
		"no origin":        {Destination: flights.Place{Name: "Lisbon"}, DepartDate: day("2026-11-12")},
		"no destination":   {Origin: flights.Place{Name: "NYC"}, DepartDate: day("2026-11-12")},
		"no depart date":   {Origin: flights.Place{Name: "NYC"}, Destination: flights.Place{Name: "Lisbon"}},
		"return before":    {Origin: flights.Place{Name: "NYC"}, Destination: flights.Place{Name: "Lisbon"}, DepartDate: day("2026-11-12"), ReturnDate: &back},
		"bad iata":         {Origin: flights.Place{Name: "NYC", IATA: "jfk"}, Destination: flights.Place{Name: "Lisbon"}, DepartDate: day("2026-11-12")},
		"ten passengers":   {Origin: flights.Place{Name: "NYC"}, Destination: flights.Place{Name: "Lisbon"}, DepartDate: day("2026-11-12"), Passengers: 10},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := appendFlight(threeDayLisbon(), f)
			require.ErrorIs(t, err, ErrInvalidEdit)
		})
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/domain/trip/ -run 'TestApplyDates|TestUpsertStay|TestHasCity|TestRemoveStay|TestAppend'`
Expected: FAIL to compile with `undefined: applyDates`.

- [ ] **Step 3: Implement**

```go
package trip

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
)

// ErrInvalidEdit wraps every rule a plan edit can break. The handler answers
// InvalidArgument for it; the chat agent (plan 2) turns it into a card that
// says what was wrong.
var ErrInvalidEdit = errors.New("invalid trip edit")

// MaxTripSpanDays is the longest trip, inclusive, matching pkg/tripspan.
const MaxTripSpanDays = 30

const maxPassengers = 9

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidEdit, fmt.Sprintf(format, a...))
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// applyDates dates the trip and stamps each day: day N falls on start+N-1.
// Days are never added or dropped here. A 3-day plan inside a 5-day trip is a
// real choice (arrive late, leave early); re-planning is a separate step the
// traveller asks for.
func applyDates(t *Trip, start, end time.Time) error {
	start, end = dateOnly(start), dateOnly(end)
	if end.Before(start) {
		return invalid("end date %s is before start date %s", end.Format(time.DateOnly), start.Format(time.DateOnly))
	}
	if span := int(end.Sub(start).Hours()/24) + 1; span > MaxTripSpanDays {
		return invalid("a trip spans at most %d days, these dates span %d", MaxTripSpanDays, span)
	}
	t.StartDate, t.EndDate = &start, &end
	for i := range t.Days {
		d := start.AddDate(0, 0, int(t.Days[i].DayNumber)-1)
		t.Days[i].Date = &d
	}
	return nil
}

func sameCity(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// HasCity reports whether name is one of the trip's cities: the primary city,
// a multi-city stop, or the city any day is spent in.
func (t *Trip) HasCity(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	if sameCity(t.CityName, name) {
		return true
	}
	for _, c := range t.Cities {
		if sameCity(c.CityName, name) {
			return true
		}
	}
	for _, d := range t.Days {
		if sameCity(d.CityName, name) {
			return true
		}
	}
	return false
}

// upsertStay sets the stay for s.CityName, replacing any stay already set for
// that city however it was spelled.
func upsertStay(t *Trip, s TripStay) error {
	s.CityName = strings.TrimSpace(s.CityName)
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		return invalid("a stay needs a name")
	}
	if !t.HasCity(s.CityName) {
		return invalid("%q is not a city on this trip", s.CityName)
	}
	if s.CheckIn != nil && s.CheckOut != nil && s.CheckOut.Before(*s.CheckIn) {
		return invalid("check-out is before check-in")
	}
	for i := range t.Stays {
		if sameCity(t.Stays[i].CityName, s.CityName) {
			s.ID = t.Stays[i].ID
			t.Stays[i] = s
			return nil
		}
	}
	t.Stays = append(t.Stays, s)
	return nil
}

func removeStay(t *Trip, city string) error {
	for i := range t.Stays {
		if sameCity(t.Stays[i].CityName, city) {
			t.Stays = append(t.Stays[:i], t.Stays[i+1:]...)
			return nil
		}
	}
	return invalid("no stay is set for %q", city)
}

// appendFlight adds f under a fresh id. Any id the caller set is discarded:
// ids are the server's, so RemoveFlight can trust them.
func appendFlight(t *Trip, f TripFlight) (TripFlight, error) {
	f.Origin.Name = strings.TrimSpace(f.Origin.Name)
	f.Destination.Name = strings.TrimSpace(f.Destination.Name)
	switch {
	case f.Origin.Name == "":
		return TripFlight{}, invalid("a flight needs an origin")
	case f.Destination.Name == "":
		return TripFlight{}, invalid("a flight needs a destination")
	case f.DepartDate.IsZero():
		return TripFlight{}, invalid("a flight needs a departure date")
	case f.ReturnDate != nil && f.ReturnDate.Before(f.DepartDate):
		return TripFlight{}, invalid("the return is before the departure")
	case f.Origin.IATA != "" && !flights.ValidIATA(f.Origin.IATA),
		f.Destination.IATA != "" && !flights.ValidIATA(f.Destination.IATA):
		return TripFlight{}, invalid("airport codes are three capital letters")
	case f.Passengers > maxPassengers:
		return TripFlight{}, invalid("at most %d passengers", maxPassengers)
	}
	if f.Passengers < 1 {
		f.Passengers = 1
	}
	f.ID = uuid.New()
	t.Flights = append(t.Flights, f)
	return f, nil
}

func removeFlight(t *Trip, id uuid.UUID) error {
	for i := range t.Flights {
		if t.Flights[i].ID == id {
			t.Flights = append(t.Flights[:i], t.Flights[i+1:]...)
			return nil
		}
	}
	return invalid("no flight %s on this trip", id)
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `go test ./internal/domain/trip/ -run 'TestApplyDates|TestUpsertStay|TestHasCity|TestRemoveStay|TestAppend'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/trip/plan.go internal/domain/trip/plan_test.go
git commit -m "feat(trip): rules for trip dates, stays and flights"
```

---

### Task 7: `PlanRepository.SavePlan`

**Files:**
- Create: `internal/domain/trip/plan_repository.go`
- Test: append to `internal/domain/trip/plan_integration_test.go`

**Interfaces:**
- Consumes: `insertSnapshot`, `(*repository).GetTrip` and `loadPlan` (Task 5), and `ErrNotFound` / `ErrVersionConflict`.
- Produces:
  - `type PlanRepository interface{ SavePlan(ctx context.Context, t *Trip, baseVersion int64) (*Trip, error) }`
  - `func NewPlanRepository(db *pgxpool.Pool, logger *slog.Logger) PlanRepository`

- [ ] **Step 1: Write the failing test** (append to `plan_integration_test.go`)

```go
func TestPlanRepository_SavePlan(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewRepository(testTripDB, logger)
	plans := NewPlanRepository(testTripDB, logger)
	userID := newTripUser(t, "plan-save-"+uuid.NewString()+"@loci.test")

	tr, err := repo.SaveTrip(ctx, &Trip{
		UserID: userID, CityName: "Lisbon", Title: "Lisbon",
		Days: []TripDay{
			{DayNumber: 1, CityName: "Lisbon", Stops: []TripStop{{Name: "Belém", OrderIndex: 0}}},
			{DayNumber: 2, CityName: "Lisbon"},
		},
	}, 0)
	require.NoError(t, err)
	stopID := tr.Days[0].Stops[0].ID

	require.NoError(t, applyDates(tr, day("2026-11-12"), day("2026-11-13")))
	require.NoError(t, upsertStay(tr, TripStay{CityName: "Lisbon", Name: "Hotel Avenida", StarRating: "4"}))
	f, err := appendFlight(tr, TripFlight{
		Origin: flights.Place{Name: "New York", IATA: "JFK"}, Destination: flights.Place{Name: "Lisbon", IATA: "LIS"},
		DepartDate: day("2026-11-12"), Links: []flights.Link{{Provider: "google_flights", Label: "Google Flights", URL: "https://example.test"}},
	})
	require.NoError(t, err)

	saved, err := plans.SavePlan(ctx, tr, tr.Version)
	require.NoError(t, err)
	require.Equal(t, tr.Version+1, saved.Version)
	require.Equal(t, "2026-11-13", saved.Days[1].Date.Format(time.DateOnly))
	require.Equal(t, stopID, saved.Days[0].Stops[0].ID, "only dates move; stops keep their ids")
	require.Len(t, saved.Stays, 1)
	require.Len(t, saved.Flights, 1)
	require.Equal(t, f.ID, saved.Flights[0].ID, "flight ids survive a save")
	require.Equal(t, "https://example.test", saved.Flights[0].Links[0].URL)

	// A second save of the stale copy is refused and changes nothing.
	tr.Stays = nil
	_, err = plans.SavePlan(ctx, tr, tr.Version)
	require.ErrorIs(t, err, ErrVersionConflict)
	require.Len(t, mustGet(t, repo, tr.ID, userID).Stays, 1)

	// Someone else's trip is not found, not overwritten.
	other := *saved
	other.UserID = newTripUser(t, "plan-other-"+uuid.NewString()+"@loci.test")
	_, err = plans.SavePlan(ctx, &other, saved.Version)
	require.ErrorIs(t, err, ErrNotFound)
}
```

Add `"github.com/FACorreiaa/loci-connect-api/pkg/flights"` to the test file's imports.

- [ ] **Step 2: Run it to see it fail**

Run: `go test -tags=integration -p 1 -count=1 ./internal/domain/trip/ -run TestPlanRepository_SavePlan`
Expected: FAIL to compile with `undefined: NewPlanRepository`.

- [ ] **Step 3: Implement**

```go
package trip

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PlanRepository writes a trip's plan: its dates, where it sleeps and its
// flights. These live outside SaveTrip, the way sharing does. A client built
// before them sends a TripDraft without them, and SaveTrip replaces child rows
// from what it is sent, so writing them there would let any old client wipe
// them.
type PlanRepository interface {
	// SavePlan stores t's dates, day dates, stays and flights under the same
	// optimistic lock SaveTrip uses, and returns the trip as stored.
	SavePlan(ctx context.Context, t *Trip, baseVersion int64) (*Trip, error)
}

type planRepository struct {
	db    *pgxpool.Pool
	trips *repository
}

// NewPlanRepository is the Postgres PlanRepository.
func NewPlanRepository(db *pgxpool.Pool, logger *slog.Logger) PlanRepository {
	return &planRepository{
		db:    db,
		trips: &repository{db: db, logger: logger.With(slog.String("component", "trip-plan-repository"))},
	}
}

func (r *planRepository) SavePlan(ctx context.Context, t *Trip, baseVersion int64) (*Trip, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback is a no-op after commit

	var stored int64
	err = tx.QueryRow(ctx, `SELECT version FROM trips WHERE id = $1 AND user_id = $2 FOR UPDATE`,
		t.ID, t.UserID).Scan(&stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock trip: %w", err)
	}
	if stored != baseVersion {
		return nil, ErrVersionConflict
	}
	t.Version = stored + 1

	if _, err := tx.Exec(ctx, `
		UPDATE trips SET start_date = $1, end_date = $2, version = $3, updated_at = NOW() WHERE id = $4`,
		t.StartDate, t.EndDate, t.Version, t.ID); err != nil {
		return nil, fmt.Errorf("update trip dates: %w", err)
	}
	// Only day dates move. Ids, stops and order stay as SaveTrip left them,
	// so nothing a client holds (AddToTrip's day choice, iOS reminders keyed
	// by stop) goes stale.
	for _, d := range t.Days {
		if _, err := tx.Exec(ctx, `UPDATE trip_days SET date = $1 WHERE id = $2 AND trip_id = $3`,
			d.Date, d.ID, t.ID); err != nil {
			return nil, fmt.Errorf("date day %d: %w", d.DayNumber, err)
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM trip_stays WHERE trip_id = $1`, t.ID); err != nil {
		return nil, fmt.Errorf("clear stays: %w", err)
	}
	for _, s := range t.Stays {
		id := s.ID
		if id == uuid.Nil {
			id = uuid.New()
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO trip_stays (id, trip_id, city_name, poi_id, name, star_rating, check_in, check_out, booking_url)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			id, t.ID, s.CityName, s.POIID, s.Name, s.StarRating, s.CheckIn, s.CheckOut, s.BookingURL); err != nil {
			return nil, fmt.Errorf("insert stay: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM trip_flights WHERE trip_id = $1`, t.ID); err != nil {
		return nil, fmt.Errorf("clear flights: %w", err)
	}
	for _, f := range t.Flights {
		links, err := json.Marshal(f.Links)
		if err != nil {
			return nil, fmt.Errorf("marshal flight links: %w", err)
		}
		if f.Links == nil {
			links = []byte("[]")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO trip_flights (id, trip_id, origin_name, origin_iata, destination_name, destination_iata,
				depart_date, return_date, passengers, cabin, links, carrier, flight_no, price_text, notes)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
			f.ID, t.ID, f.Origin.Name, f.Origin.IATA, f.Destination.Name, f.Destination.IATA,
			f.DepartDate, f.ReturnDate, f.Passengers, int32(f.Cabin), links,
			f.Carrier, f.FlightNo, f.PriceText, f.Notes); err != nil {
			return nil, fmt.Errorf("insert flight: %w", err)
		}
	}

	if err := insertSnapshot(ctx, tx, t); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return r.trips.GetTrip(ctx, t.ID, t.UserID)
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `go test -tags=integration -p 1 -count=1 ./internal/domain/trip/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/trip/plan_repository.go internal/domain/trip/plan_integration_test.go
git commit -m "feat(trip): SavePlan writes dates, stays and flights under the trip's version lock"
```

---

### Task 8: `trip.Service`

**Files:**
- Create: `internal/domain/trip/service.go`
- Test: `internal/domain/trip/service_test.go`

**Interfaces:**
- Consumes: `Repository.GetTrip`, `PlanRepository.SavePlan`, the Task 6 rules, and `flights.Provider`.
- Produces:
  - `type Service struct{…}` and `func NewService(trips Repository, plans PlanRepository, links flights.Provider) *Service`
  - `func (s *Service) SetDates(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, start, end time.Time) (*Trip, error)`
  - `func (s *Service) SetStay(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, stay TripStay) (*Trip, error)`
  - `func (s *Service) ClearStay(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, city string) (*Trip, error)`
  - `func (s *Service) AddFlight(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, f TripFlight) (*Trip, error)`
  - `func (s *Service) RemoveFlight(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, flightID uuid.UUID) (*Trip, error)`
  - `func (s *Service) FlightLinks(q flights.Query) []flights.Link`
  - `func FlightQuery(f TripFlight) flights.Query`

  Plan 2's chat agent calls these same methods.

- [ ] **Step 1: Write the failing tests**

```go
package trip

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
)

// memTrips is an in-memory Repository + PlanRepository for service tests.
type memTrips struct {
	trip  *Trip
	saves int
}

func (m *memTrips) GetTrip(_ context.Context, id, userID uuid.UUID) (*Trip, error) {
	if m.trip == nil || m.trip.ID != id || m.trip.UserID != userID {
		return nil, ErrNotFound
	}
	cp := *m.trip
	cp.Days = append([]TripDay(nil), m.trip.Days...)
	cp.Stays = append([]TripStay(nil), m.trip.Stays...)
	cp.Flights = append([]TripFlight(nil), m.trip.Flights...)
	return &cp, nil
}

func (m *memTrips) ListTrips(context.Context, uuid.UUID, int, int) ([]*Trip, int, error) {
	return nil, 0, nil
}

func (m *memTrips) SaveTrip(context.Context, *Trip, int64) (*Trip, error) { panic("not used") }

func (m *memTrips) SetShare(context.Context, uuid.UUID, uuid.UUID, bool, string) (*Trip, error) {
	panic("not used")
}

func (m *memTrips) SavePlan(_ context.Context, t *Trip, base int64) (*Trip, error) {
	if m.trip.Version != base {
		return nil, ErrVersionConflict
	}
	m.saves++
	t.Version = base + 1
	m.trip = t
	return t, nil
}

func newServiceFixture() (*Service, *memTrips, uuid.UUID, uuid.UUID) {
	uid, tid := uuid.New(), uuid.New()
	mem := &memTrips{trip: &Trip{ID: tid, UserID: uid, CityName: "Lisbon", Version: 3,
		Days: []TripDay{{DayNumber: 1}, {DayNumber: 2}}}}
	return NewService(mem, mem, flights.DeepLinks{}), mem, uid, tid
}

func TestService_StaleVersionSavesNothing(t *testing.T) {
	svc, mem, uid, tid := newServiceFixture()
	_, err := svc.SetDates(context.Background(), uid, tid, 2, day("2026-11-12"), day("2026-11-13"))
	require.ErrorIs(t, err, ErrVersionConflict)
	require.Zero(t, mem.saves)
}

func TestService_InvalidEditSavesNothing(t *testing.T) {
	svc, mem, uid, tid := newServiceFixture()
	_, err := svc.SetStay(context.Background(), uid, tid, 3, TripStay{CityName: "Porto", Name: "X"})
	require.ErrorIs(t, err, ErrInvalidEdit)
	require.Zero(t, mem.saves)
}

func TestService_OtherUsersTripIsNotFound(t *testing.T) {
	svc, _, _, tid := newServiceFixture()
	_, err := svc.ClearStay(context.Background(), uuid.New(), tid, 3, "Lisbon")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestService_AddFlightBuildsItsOwnLinks(t *testing.T) {
	svc, _, uid, tid := newServiceFixture()
	got, err := svc.AddFlight(context.Background(), uid, tid, 3, TripFlight{
		ID:     uuid.New(),
		Origin: flights.Place{Name: "New York", IATA: "JFK"}, Destination: flights.Place{Name: "Lisbon", IATA: "LIS"},
		DepartDate: day("2026-11-12"), Passengers: 2,
		Links: []flights.Link{{Provider: "evil", URL: "https://phish.example"}},
	})
	require.NoError(t, err)
	require.Len(t, got.Flights, 1)
	links := got.Flights[0].Links
	require.Len(t, links, 2)
	require.Equal(t, "google_flights", links[0].Provider)
	require.Equal(t, "skyscanner", links[1].Provider)
	for _, l := range links {
		require.NotContains(t, l.URL, "phish")
	}
}

func TestService_SetDatesThenRemoveFlightRoundTrip(t *testing.T) {
	svc, _, uid, tid := newServiceFixture()
	ctx := context.Background()
	tr, err := svc.SetDates(ctx, uid, tid, 3, day("2026-11-12"), day("2026-11-13"))
	require.NoError(t, err)
	tr, err = svc.AddFlight(ctx, uid, tid, tr.Version, TripFlight{
		Origin: flights.Place{Name: "Porto"}, Destination: flights.Place{Name: "Lisbon"}, DepartDate: day("2026-11-12"),
	})
	require.NoError(t, err)
	tr, err = svc.RemoveFlight(ctx, uid, tid, tr.Version, tr.Flights[0].ID)
	require.NoError(t, err)
	require.Empty(t, tr.Flights)
	require.EqualValues(t, 6, tr.Version)
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/domain/trip/ -run TestService_`
Expected: FAIL to compile with `undefined: NewService`.

- [ ] **Step 3: Implement**

```go
package trip

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
)

// Service is the one write path for a trip's plan. The Connect handler calls
// it, and so will the chat agent and Telegram (plan 2), so ownership, the
// version lock and the edit rules are checked in one place whoever asks.
type Service struct {
	trips Repository
	plans PlanRepository
	links flights.Provider
}

func NewService(trips Repository, plans PlanRepository, links flights.Provider) *Service {
	return &Service{trips: trips, plans: plans, links: links}
}

// editPlan loads the caller's trip, refuses a stale base version before doing
// any work, applies fn and saves. fn's error is returned as-is (it wraps
// ErrInvalidEdit), and nothing is saved.
func (s *Service) editPlan(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, fn func(*Trip) error) (*Trip, error) {
	t, err := s.trips.GetTrip(ctx, tripID, userID)
	if err != nil {
		return nil, err
	}
	if t.Version != baseVersion {
		return nil, ErrVersionConflict
	}
	if err := fn(t); err != nil {
		return nil, err
	}
	return s.plans.SavePlan(ctx, t, baseVersion)
}

func (s *Service) SetDates(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, start, end time.Time) (*Trip, error) {
	return s.editPlan(ctx, userID, tripID, baseVersion, func(t *Trip) error { return applyDates(t, start, end) })
}

func (s *Service) SetStay(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, stay TripStay) (*Trip, error) {
	return s.editPlan(ctx, userID, tripID, baseVersion, func(t *Trip) error { return upsertStay(t, stay) })
}

func (s *Service) ClearStay(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, city string) (*Trip, error) {
	return s.editPlan(ctx, userID, tripID, baseVersion, func(t *Trip) error { return removeStay(t, city) })
}

// AddFlight saves f with links the server builds. Links a caller sends are
// dropped: they are shown to friends as tappable buttons, so they must be ours.
func (s *Service) AddFlight(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, f TripFlight) (*Trip, error) {
	return s.editPlan(ctx, userID, tripID, baseVersion, func(t *Trip) error {
		added, err := appendFlight(t, f)
		if err != nil {
			return err
		}
		t.Flights[len(t.Flights)-1].Links = s.links.Links(FlightQuery(added))
		return nil
	})
}

func (s *Service) RemoveFlight(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, flightID uuid.UUID) (*Trip, error) {
	return s.editPlan(ctx, userID, tripID, baseVersion, func(t *Trip) error { return removeFlight(t, flightID) })
}

// FlightLinks is the manual form's search: links only, nothing saved.
func (s *Service) FlightLinks(q flights.Query) []flights.Link { return s.links.Links(q) }

// FlightQuery is the search a saved flight stands for.
func FlightQuery(f TripFlight) flights.Query {
	return flights.Query{
		Origin: f.Origin, Destination: f.Destination,
		Depart: f.DepartDate, Return: f.ReturnDate,
		Passengers: int(f.Passengers), Cabin: f.Cabin,
	}
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `go test ./internal/domain/trip/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/trip/service.go internal/domain/trip/service_test.go
git commit -m "feat(trip): Service, the one write path for a trip's plan"
```

---

### Task 9: RPCs, mappers, redaction, wiring

**Files:**
- Create: `internal/domain/trip/plan_handler.go`
- Test: `internal/domain/trip/plan_handler_test.go`
- Modify: `internal/domain/trip/handler.go` (`Handler` struct field and `toConnectErr`), `internal/domain/trip/mappers.go` (`tripToProto`), `internal/domain/trip/sharing.go` (`redactForViewer`, around line 283), and `cmd/api/dependencies.go:895`.

**Interfaces:**
- Consumes: the `Service` methods (Task 8) and the proto types (Task 1).
- Produces:
  - `func (h *Handler) WithPlan(svc *Service) *Handler`
  - the six RPC methods on `*Handler`
  - `func stayToProto(TripStay) *tripv1.TripStay`, `func flightToProto(TripFlight) *tripv1.TripFlight`, `func flightFromProto(*tripv1.TripFlight) (TripFlight, error)`, `func stayFromProto(*tripv1.TripStay) (TripStay, error)`, `func placeFromProto(*tripv1.FlightPlace) flights.Place`, `func parseDate(string) (time.Time, error)`

  Plan 2 reuses `flightFromProto`, `stayFromProto` and `parseDate`.

- [ ] **Step 1: Write the failing tests**

```go
package trip

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

func authed(uid uuid.UUID) context.Context {
	return interceptors.ContextWithUserID(context.Background(), uid.String())
}

func TestPlanRPCs_UnimplementedWithoutService(t *testing.T) {
	h := NewHandler(&memTrips{}, "", nil, nil)
	_, err := h.SetTripDates(authed(uuid.New()), connect.NewRequest(&tripv1.SetTripDatesRequest{
		TripId: uuid.NewString(), StartDate: "2026-11-12", EndDate: "2026-11-13",
	}))
	require.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
}

func TestPlanRPCs_ErrorCodes(t *testing.T) {
	svc, mem, uid, tid := newServiceFixture()
	h := NewHandler(mem, "", nil, nil).WithPlan(svc)
	ctx := authed(uid)

	_, err := h.SetTripDates(ctx, connect.NewRequest(&tripv1.SetTripDatesRequest{
		TripId: tid.String(), StartDate: "2026-11-12", EndDate: "2026-11-13", BaseVersion: 1,
	}))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "stale version")

	_, err = h.SetStay(ctx, connect.NewRequest(&tripv1.SetStayRequest{
		TripId: tid.String(), BaseVersion: 3, Stay: &tripv1.TripStay{CityName: "Porto", Name: "X"},
	}))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "city not on trip")

	_, err = h.RemoveFlight(ctx, connect.NewRequest(&tripv1.RemoveFlightRequest{
		TripId: tid.String(), BaseVersion: 3, FlightId: "not-a-uuid",
	}))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestPlanRPCs_SetDatesAndAddFlight(t *testing.T) {
	svc, mem, uid, tid := newServiceFixture()
	h := NewHandler(mem, "", nil, nil).WithPlan(svc)
	ctx := authed(uid)

	res, err := h.SetTripDates(ctx, connect.NewRequest(&tripv1.SetTripDatesRequest{
		TripId: tid.String(), StartDate: "2026-11-12", EndDate: "2026-11-13", BaseVersion: 3,
	}))
	require.NoError(t, err)
	require.Equal(t, "2026-11-12", res.Msg.GetStartDate())
	require.Equal(t, "2026-11-13", res.Msg.GetDays()[1].GetDate().AsTime().Format("2006-01-02"))

	iata := "LIS"
	res, err = h.AddFlight(ctx, connect.NewRequest(&tripv1.AddFlightRequest{
		TripId: tid.String(), BaseVersion: res.Msg.GetVersion(),
		Flight: &tripv1.TripFlight{
			Origin:      &tripv1.FlightPlace{Name: "Porto"},
			Destination: &tripv1.FlightPlace{Name: "Lisbon", Iata: &iata},
			DepartDate:  "2026-11-12", Passengers: 1,
			Links:       []*tripv1.FlightLink{{Provider: "evil", Url: "https://phish.example"}},
		},
	}))
	require.NoError(t, err)
	require.Len(t, res.Msg.GetFlights(), 1)
	require.Equal(t, "google_flights", res.Msg.GetFlights()[0].GetLinks()[0].GetProvider())
	require.NotEmpty(t, res.Msg.GetFlights()[0].GetId())
}

func TestBuildFlightLinks(t *testing.T) {
	svc, mem, uid, _ := newServiceFixture()
	h := NewHandler(mem, "", nil, nil).WithPlan(svc)
	ret := "2026-11-17"
	res, err := h.BuildFlightLinks(authed(uid), connect.NewRequest(&tripv1.BuildFlightLinksRequest{
		Origin:      &tripv1.FlightPlace{Name: "New York"},
		Destination: &tripv1.FlightPlace{Name: "Lisbon"},
		DepartDate:  "2026-11-10", ReturnDate: &ret, Passengers: 1,
	}))
	require.NoError(t, err)
	require.Len(t, res.Msg.GetLinks(), 1, "names only: Google, no Skyscanner")
}

func TestRedactForViewer_HidesPlanDetails(t *testing.T) {
	booking, notes, price, no, carrier := "https://hotel.example", "door code 1234", "€420", "TP 202", "TAP"
	p := tripToProto(&Trip{
		Stays: []TripStay{{CityName: "Lisbon", Name: "Hotel Avenida", BookingURL: &booking}},
		Flights: []TripFlight{{
			Origin: flights.Place{Name: "NYC"}, Destination: flights.Place{Name: "Lisbon"}, DepartDate: day("2026-11-12"),
			Notes: &notes, PriceText: &price, FlightNo: &no, Carrier: &carrier,
		}},
	})
	got := redactForViewer(p, false, nil)
	require.Nil(t, got.GetStays()[0].BookingUrl)
	f := got.GetFlights()[0]
	require.Nil(t, f.Notes)
	require.Nil(t, f.PriceText)
	require.Nil(t, f.FlightNo)
	require.Nil(t, f.Carrier)
	require.Equal(t, "Hotel Avenida", got.GetStays()[0].GetName(), "the stay itself is still shown")
}
```

Before Step 2, check the context helper name: `git grep -n "func .*Context.*UserID" pkg/interceptors/`. If the setter is named something other than `ContextWithUserID`, use the real name in `authed`. Do **not** add a helper to `interceptors`.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/domain/trip/ -run 'TestPlanRPCs|TestBuildFlightLinks|TestRedactForViewer_HidesPlanDetails'`
Expected: FAIL to compile with `h.WithPlan undefined`.

- [ ] **Step 3: Handler field and error mapping**

In `handler.go`, add to the `Handler` struct after `graph SocialGraph`:

```go
	// Optional, attached via WithPlan. Nil makes the plan RPCs (dates, stays,
	// flights) answer Unimplemented.
	plan *Service
```

In `toConnectErr`, add a case before `default`:

```go
	case errors.Is(err, ErrInvalidEdit):
		return connect.NewError(connect.CodeInvalidArgument, err)
```

- [ ] **Step 4: Mappers**

In `mappers.go`, at the end of `tripToProto` before `return p`:

```go
	if t.StartDate != nil {
		s := t.StartDate.Format(time.DateOnly)
		p.StartDate = &s
	}
	if t.EndDate != nil {
		s := t.EndDate.Format(time.DateOnly)
		p.EndDate = &s
	}
	for _, s := range t.Stays {
		p.Stays = append(p.Stays, stayToProto(s))
	}
	for _, f := range t.Flights {
		p.Flights = append(p.Flights, flightToProto(f))
	}
```

Then add these to `mappers.go`, and add `"github.com/FACorreiaa/loci-connect-api/pkg/flights"` to its imports:

```go
func parseDate(s string) (time.Time, error) {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %q is not a YYYY-MM-DD date", ErrInvalidEdit, s)
	}
	return d, nil
}

func parseOptionalDate(s *string) (*time.Time, error) {
	if s == nil {
		return nil, nil
	}
	d, err := parseDate(*s)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func formatOptionalDate(d *time.Time) *string {
	if d == nil {
		return nil
	}
	s := d.Format(time.DateOnly)
	return &s
}

func stayToProto(s TripStay) *tripv1.TripStay {
	return &tripv1.TripStay{
		CityName: s.CityName, PoiId: s.POIID, Name: s.Name, StarRating: s.StarRating,
		CheckIn: formatOptionalDate(s.CheckIn), CheckOut: formatOptionalDate(s.CheckOut),
		BookingUrl: s.BookingURL,
	}
}

func stayFromProto(p *tripv1.TripStay) (TripStay, error) {
	in, err := parseOptionalDate(p.CheckIn)
	if err != nil {
		return TripStay{}, err
	}
	out, err := parseOptionalDate(p.CheckOut)
	if err != nil {
		return TripStay{}, err
	}
	return TripStay{
		CityName: p.GetCityName(), POIID: p.GetPoiId(), Name: p.GetName(), StarRating: p.GetStarRating(),
		CheckIn: in, CheckOut: out, BookingURL: p.BookingUrl,
	}, nil
}

func placeFromProto(p *tripv1.FlightPlace) flights.Place {
	return flights.Place{Name: p.GetName(), IATA: p.GetIata()}
}

func placeToProto(p flights.Place) *tripv1.FlightPlace {
	return &tripv1.FlightPlace{Name: p.Name, Iata: stringPtrOrNil(p.IATA)}
}

// flightFromProto reads what a client may set. id and links are ignored:
// the server assigns one and builds the other.
func flightFromProto(p *tripv1.TripFlight) (TripFlight, error) {
	depart, err := parseDate(p.GetDepartDate())
	if err != nil {
		return TripFlight{}, err
	}
	ret, err := parseOptionalDate(p.ReturnDate)
	if err != nil {
		return TripFlight{}, err
	}
	return TripFlight{
		Origin: placeFromProto(p.GetOrigin()), Destination: placeFromProto(p.GetDestination()),
		DepartDate: depart, ReturnDate: ret,
		Passengers: p.GetPassengers(), Cabin: flights.Cabin(p.GetCabin()),
		Carrier: p.Carrier, FlightNo: p.FlightNo, PriceText: p.PriceText, Notes: p.Notes,
	}, nil
}

func flightToProto(f TripFlight) *tripv1.TripFlight {
	p := &tripv1.TripFlight{
		Id:     f.ID.String(),
		Origin: placeToProto(f.Origin), Destination: placeToProto(f.Destination),
		DepartDate: f.DepartDate.Format(time.DateOnly), ReturnDate: formatOptionalDate(f.ReturnDate),
		Passengers: f.Passengers, Cabin: tripv1.FlightCabin(f.Cabin),
		Carrier: f.Carrier, FlightNo: f.FlightNo, PriceText: f.PriceText, Notes: f.Notes,
	}
	for _, l := range f.Links {
		p.Links = append(p.Links, &tripv1.FlightLink{Provider: l.Provider, Label: l.Label, Url: l.URL})
	}
	return p
}
```

Check `stringPtrOrNil` (`mappers.go:337`): it must return nil for `""`. If it doesn't, write a local `if p.IATA != ""` instead.

- [ ] **Step 5: The RPCs**

```go
package trip

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
)

// WithPlan attaches the trip plan (dates, stays, flights). Without it those
// RPCs answer Unimplemented.
func (h *Handler) WithPlan(svc *Service) *Handler {
	h.plan = svc
	return h
}

// planTarget resolves the caller and the trip a plan RPC edits.
func (h *Handler) planTarget(ctx context.Context, tripID string) (uuid.UUID, uuid.UUID, error) {
	if h.plan == nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeUnimplemented, errors.New("trip plans are not enabled"))
	}
	uid, err := userID(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	id, err := uuid.Parse(tripID)
	if err != nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid trip ID"))
	}
	return uid, id, nil
}

func (h *Handler) planResult(ctx context.Context, t *Trip, err error) (*connect.Response[tripv1.TripDraft], error) {
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(h.respond(ctx, t)), nil
}

func (h *Handler) SetTripDates(ctx context.Context, req *connect.Request[tripv1.SetTripDatesRequest]) (*connect.Response[tripv1.TripDraft], error) {
	uid, id, err := h.planTarget(ctx, req.Msg.GetTripId())
	if err != nil {
		return nil, err
	}
	start, err := parseDate(req.Msg.GetStartDate())
	if err != nil {
		return nil, toConnectErr(err)
	}
	end, err := parseDate(req.Msg.GetEndDate())
	if err != nil {
		return nil, toConnectErr(err)
	}
	t, err := h.plan.SetDates(ctx, uid, id, req.Msg.GetBaseVersion(), start, end)
	return h.planResult(ctx, t, err)
}

func (h *Handler) SetStay(ctx context.Context, req *connect.Request[tripv1.SetStayRequest]) (*connect.Response[tripv1.TripDraft], error) {
	uid, id, err := h.planTarget(ctx, req.Msg.GetTripId())
	if err != nil {
		return nil, err
	}
	stay, err := stayFromProto(req.Msg.GetStay())
	if err != nil {
		return nil, toConnectErr(err)
	}
	t, err := h.plan.SetStay(ctx, uid, id, req.Msg.GetBaseVersion(), stay)
	return h.planResult(ctx, t, err)
}

func (h *Handler) ClearStay(ctx context.Context, req *connect.Request[tripv1.ClearStayRequest]) (*connect.Response[tripv1.TripDraft], error) {
	uid, id, err := h.planTarget(ctx, req.Msg.GetTripId())
	if err != nil {
		return nil, err
	}
	t, err := h.plan.ClearStay(ctx, uid, id, req.Msg.GetBaseVersion(), req.Msg.GetCityName())
	return h.planResult(ctx, t, err)
}

func (h *Handler) AddFlight(ctx context.Context, req *connect.Request[tripv1.AddFlightRequest]) (*connect.Response[tripv1.TripDraft], error) {
	uid, id, err := h.planTarget(ctx, req.Msg.GetTripId())
	if err != nil {
		return nil, err
	}
	f, err := flightFromProto(req.Msg.GetFlight())
	if err != nil {
		return nil, toConnectErr(err)
	}
	t, err := h.plan.AddFlight(ctx, uid, id, req.Msg.GetBaseVersion(), f)
	return h.planResult(ctx, t, err)
}

func (h *Handler) RemoveFlight(ctx context.Context, req *connect.Request[tripv1.RemoveFlightRequest]) (*connect.Response[tripv1.TripDraft], error) {
	uid, id, err := h.planTarget(ctx, req.Msg.GetTripId())
	if err != nil {
		return nil, err
	}
	flightID, err := uuid.Parse(req.Msg.GetFlightId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid flight ID"))
	}
	t, err := h.plan.RemoveFlight(ctx, uid, id, req.Msg.GetBaseVersion(), flightID)
	return h.planResult(ctx, t, err)
}

func (h *Handler) BuildFlightLinks(ctx context.Context, req *connect.Request[tripv1.BuildFlightLinksRequest]) (*connect.Response[tripv1.BuildFlightLinksResponse], error) {
	if h.plan == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("trip plans are not enabled"))
	}
	if _, err := userID(ctx); err != nil {
		return nil, err
	}
	depart, err := parseDate(req.Msg.GetDepartDate())
	if err != nil {
		return nil, toConnectErr(err)
	}
	ret, err := parseOptionalDate(req.Msg.ReturnDate)
	if err != nil {
		return nil, toConnectErr(err)
	}
	links := h.plan.FlightLinks(flights.Query{
		Origin: placeFromProto(req.Msg.GetOrigin()), Destination: placeFromProto(req.Msg.GetDestination()),
		Depart: depart, Return: ret,
		Passengers: int(req.Msg.GetPassengers()), Cabin: flights.Cabin(req.Msg.GetCabin()),
	})
	res := &tripv1.BuildFlightLinksResponse{}
	for _, l := range links {
		res.Links = append(res.Links, &tripv1.FlightLink{Provider: l.Provider, Label: l.Label, Url: l.URL})
	}
	return connect.NewResponse(res), nil
}
```

- [ ] **Step 6: Redaction**

In `sharing.go` `redactForViewer`, inside `if !shareDetails { … }` after the legs loop:

```go
		for _, s := range p.GetStays() {
			s.BookingUrl = nil
		}
		// What the traveller typed about a flight can place them (seat-level
		// timing, a booking reference in notes); friends see the route and date.
		for _, f := range p.GetFlights() {
			f.Notes = nil
			f.PriceText = nil
			f.FlightNo = nil
			f.Carrier = nil
		}
```

- [ ] **Step 7: Wire it**

In `cmd/api/dependencies.go`, change line 895 to:

```go
	d.TripHandler = trip.NewHandler(d.TripRepo, d.Config.Server.BaseURL, d.PreferenceRecorder, d.SubscriptionService).
		WithPlaces(d.POIRepo).
		WithPlan(trip.NewService(d.TripRepo, trip.NewPlanRepository(d.DB.Pool, d.Logger), flights.DeepLinks{}))
```

Then add the import `"github.com/FACorreiaa/loci-connect-api/pkg/flights"`. Expose the service for plan 2 by adding a `TripService *trip.Service` field next to `TripHandler` and assigning it before the handler line.

- [ ] **Step 8: Run everything**

Run: `go build ./... && go test ./internal/domain/trip/ ./pkg/flights/ && go test -tags=integration -p 1 -count=1 ./internal/domain/trip/ && make lint-go`
Expected: PASS and `0 issues.`

- [ ] **Step 9: Commit and open the PR**

```bash
git add internal/domain/trip/plan_handler.go internal/domain/trip/plan_handler_test.go \
  internal/domain/trip/handler.go internal/domain/trip/mappers.go internal/domain/trip/sharing.go \
  cmd/api/dependencies.go
git commit -m "feat(trip): SetTripDates, SetStay/ClearStay, AddFlight/RemoveFlight and BuildFlightLinks"
git push -u origin feat/trip-plan && gh pr create --fill
```

---

### Task 10: Deploy and prove it

- [ ] **Step 1:** After the PR merges, CD builds the image. If CD still opens no promote PR (the `INFRA_TOKEN` problem), open one by hand in `~/Work/production/platform/infra`, as #230 did. Bump `apps/loci/api/values-production.yaml` `image.tag`, plus the API image in `apps/loci/data/preference-rerank-cronjob.yaml` and `bundle-forge-job.yaml`. The migration needs no new Postgres extension, so the Postgres pin stays.
- [ ] **Step 2:** After ArgoCD syncs, probe each new RPC without credentials:

```bash
for m in SetTripDates SetStay ClearStay AddFlight RemoveFlight BuildFlightLinks; do
  printf '%s ' $m; curl -s -o /dev/null -w '%{http_code}\n' -X POST -H 'Content-Type: application/json' -d '{}' \
    https://api.lociai.fyi/loci.trip.TripService/$m
done
```

Expected: every line ends in `401`. A `404` means the image without these RPCs is still running. A `400` means protovalidate ran before auth, as it does today. Both 400 and 401 prove the route is mounted.

- [ ] **Step 3:** Signed in, from Bruno (`loci-collection-generated/loci_trip_TripService/`, regenerated by `make bruno` in Task 1), call `SetTripDates` then `AddFlight` on a trip of your own. Open the returned Google Flights link and check it is prefilled.

---

## Spec deviations (deliberate)

- A version conflict answers `FailedPrecondition`, not `Aborted`, to match every existing trip mutation.
- `RegenerateDays` and the `trip_action_proposals` table move to plan 2, which owns generation and proposals.
- Plan writes go through `PlanRepository.SavePlan`, not `SaveTrip`. The reason is the old-client wipe risk (Review Focus 1).
- `AddFlight` ignores client-sent links and ids. The spec said only "server builds links".
