# Trip agent actions (chat proposes, traveller confirms) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** On a chat turn bound to a trip, the agent turns "4-star hotels in Lisbon 12–17 Nov, make it 6 days, flights from New York" into proposal cards. Confirming a card changes the trip through `trip.Service`.

**Architecture:**
- A new domain package, `internal/domain/tripaction`, does four things:
  - It extracts typed actions with one JSON LLM call.
  - It validates them.
  - It resolves options: hotels by stars through the existing `poi.Service.GetNearbyHotels`, and flight links through `trip.Service.FlightLinks`.
  - It stores proposals in Postgres and applies a confirmed one through `trip.Service`.
- `StreamChat` turns that carry `trip_id` call it first:
  - If it proposes anything, the turn emits `action_proposal` events and completes, with no regeneration.
  - Otherwise the turn is answered as usual, but never saved as a new trip.
- `ApplyTripAction` and `DismissTripAction` are new `ChatService` RPCs.
- "Re-plan as N days" runs the same per-city generation a multi-city stop uses, then swaps the trip's days through a new `trip.Service.ReplaceDays`.

**Tech Stack:** Go, connect-go, protovalidate, pgx v5, goose, Buf/BSR (`loci/loci-proto`), testify, google.golang.org/genai via `go-genai-sdk`.

**Spec:** `docs/superpowers/specs/2026-09-30-trip-workflow-agent-actions-design.md` (on main). This is plan 2 of 5. Plan 1 (dates, stays, flights; api #109) is merged. Plans 3–5 are web, iOS and Telegram.

## Global Constraints

- Propose, then confirm. **Nothing the agent extracts changes a trip until `ApplyTripAction`.**
- **No native tool calling.** Extraction is one `GenerateText` call returning `{"actions":[...]}`. Validate it in Go, retry once on unparsable JSON, then give up and return no actions.
- **Extraction failure never blocks the answer.** On any error the turn falls through to normal generation.
- **A trip-bound turn never creates a new trip.**
- Proposals live server-side, are owned by one user, are applied at most once, and expire after **24h**.
- A stale `base_version` returns **`FailedPrecondition`**, as for every trip edit.
- Action set for v1: **`set_dates`, `search_hotels`, `regenerate_days`, `search_flights`**.
- Proto is consumed as a tagged release. After bumping, run `go mod tidy && make generate`. Run `buf push` from a clean checkout of the tag. **Check the newest tag first. Other sessions cut tags too: v5.34.0 is the boards proto.**
- Adding a `StreamEvent.payload` oneof case breaks iOS `main` until iOS handles it, so Task 12 ships with the proto.
- Another session commits in the same checkouts. Work in worktrees and `git add` explicit paths.
- Integration tests on this Mac need `DOCKER_HOST=unix:///Users/fernandocorreiachill/.orbstack/run/docker.sock TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock TESTCONTAINERS_RYUK_DISABLED=true`.

## Review Focus

1. **A trip-bound message that asks for no change** ("what's good for dinner?") gets the usual answer and no new trip. Pinned in Task 10.
2. **The LLM is down, or replies with prose or broken JSON.** The answer still streams with no cards. Pinned in Tasks 6 and 10.
3. **ApplyTripAction misuse:** a double tap, an apply after dismiss, an expired proposal, or someone else's proposal. Each causes no second change and returns the right code. Pinned in Tasks 8 and 11.
4. **A re-plan applied after the trip moved on** is refused before the slow generation runs. A failed re-plan gives the proposal back. Pinned in Task 8.
5. **Hotels with odd star text or links:**
   - Star text such as `"4.5"`, `"4 stars"`, `"★★★★"` or `""` is filtered correctly.
   - An LLM-generated hotel with a nil id gets an empty `poi_id`.
   - An `http://` website never becomes a booking link.
   - Pinned in Tasks 5 and 8.

## File map

**loci-connect-proto**
- Modify `proto/loci/chat/chat.proto` to add the action messages, payload 34, enum value 15 and two RPCs.

**loci-connect-server**
- Create `pkg/db/migrations/0110_trip_action_proposals.up.sql`.
- Modify `internal/domain/trip/service.go` (add `ReplaceDays`), `internal/domain/trip/mappers.go` (exported mappers) and `internal/domain/trip/service_test.go`.
- Create `internal/domain/tripaction/action.go`, `stars.go`, `extract.go`, `store.go`, `service.go` and `proto.go`, each with a `_test.go`, plus `store_integration_test.go`.
- Modify `internal/types/chat_session.go` to add `EventTypeActionProposal`.
- Create `internal/domain/chat/service/chat_trip_actions.go` and its test. Modify `chat_service.go` (one field) and `chat_stream_session.go` (the hook).
- Create `internal/domain/chat/handler/trip_actions.go` and its test. Modify `chat_handler.go`: one field, one `mapEventToProto` case and one `eventTypeToProto` case.
- Modify `cmd/api/dependencies.go`, inside the `if d.DB != nil` block after `d.TripService` is built.

**loci-ios**
- Handle the new payload case as a no-op (Task 12).

---

### Task 1: Proto contracts

**Files:**
- Modify `proto/loci/chat/chat.proto`. Add messages before `service ChatService` (around line 1030), add the oneof field after `GastronomyPayload gastronomy = 33;`, add the enum value after `STREAM_EVENT_TYPE_GASTRONOMY = 14;`, and add RPCs after `rpc GetRunStatus` (around line 1051).

**Interfaces:**
- Produces `chatv1`:
  - Messages: `TripAction` (oneof `Kind`: `SetDates`, `SearchHotels`, `RegenerateDays`, `SearchFlights`), `SetDatesAction`, `SearchHotelsAction`, `RegenerateDaysAction`, `SearchFlightsAction`, `ActionOption` (oneof `Choice`: `Stay`, `Flight`), `ActionProposal`, `ActionProposalPayload`, `ApplyTripActionRequest`/`Response`, `DismissTripActionRequest`/`Response`.
  - `StreamEvent_ActionProposal` and `StreamEventType_STREAM_EVENT_TYPE_ACTION_PROPOSAL`.
  - `ChatServiceHandler.ApplyTripAction` and `ChatServiceHandler.DismissTripAction`.

- [ ] **Step 1: Worktree**

```bash
cd ~/Work/production/apps/Loci/loci-connect-proto && git fetch origin --tags
git worktree add -b feat/trip-agent-actions /private/tmp/proto-agent-actions origin/main && cd /private/tmp/proto-agent-actions
grep -n '^import' proto/loci/chat/chat.proto   # add "loci/trip/trip.proto" if absent
grep -n 'import' proto/loci/trip/trip.proto      # must not import loci/chat (no cycle)
```

- [ ] **Step 2: Add the messages before `service ChatService {`**

```proto
// Trip actions: the agent proposes, the traveller confirms. On a chat turn
// bound to a trip, the server turns the message into ActionProposals (stored
// server-side, one per change) and streams each as action_proposal. Nothing
// changes until ApplyTripAction; DismissTripAction drops one.
message TripAction {
  oneof kind {
    SetDatesAction set_dates = 1;
    SearchHotelsAction search_hotels = 2;
    RegenerateDaysAction regenerate_days = 3;
    SearchFlightsAction search_flights = 4;
  }
}

message SetDatesAction {
  string start_date = 1;
  string end_date = 2;
}

// 0..0 means any star rating; 4..4 means exactly four.
message SearchHotelsAction {
  string city_name = 1;
  int32 min_stars = 2;
  int32 max_stars = 3;
}

// Re-plans the trip's itinerary as this many days, keeping its dates, stays
// and flights.
message RegenerateDaysAction {
  int32 days = 1;
}

message SearchFlightsAction {
  loci.trip.FlightPlace origin = 1;
  loci.trip.FlightPlace destination = 2;
  string depart_date = 3;
  optional string return_date = 4;
  int32 passengers = 5;
  loci.trip.FlightCabin cabin = 6;
}

// ActionOption is one choice of a pick-one action: a hotel to stay at, or the
// flight search to save. Write actions (dates, re-plan) have none.
message ActionOption {
  string label = 1;
  string detail = 2;
  oneof choice {
    loci.trip.TripStay stay = 3;
    loci.trip.TripFlight flight = 4;
  }
}

message ActionProposal {
  string id = 1;
  string trip_id = 2;
  TripAction action = 3;
  // One line the card shows, e.g. "4★ hotels in Lisbon: pick one to stay at".
  string summary = 4;
  repeated ActionOption options = 5;
  google.protobuf.Timestamp expires_at = 6;
}

message ActionProposalPayload {
  ActionProposal proposal = 1;
}

message ApplyTripActionRequest {
  string proposal_id = 1 [(buf.validate.field).string.uuid = true];
  // Required for actions with options; ignored otherwise.
  optional int32 option_index = 2 [(buf.validate.field).int32.gte = 0];
  // The trip version the card was shown against.
  int64 base_version = 3 [(buf.validate.field).int64.gte = 0];
}

message ApplyTripActionResponse {
  loci.trip.TripDraft trip = 1;
  // Posted into the chat thread when the proposal came from one.
  ConversationMessage confirmation = 2;
}

message DismissTripActionRequest {
  string proposal_id = 1 [(buf.validate.field).string.uuid = true];
}

message DismissTripActionResponse {}
```

- [ ] **Step 3: Stream wiring**

Add after `GastronomyPayload gastronomy = 33;` in `StreamEvent.payload`:

```proto
    ActionProposalPayload action_proposal = 34;
```

Add after `STREAM_EVENT_TYPE_GASTRONOMY = 14;` in `enum StreamEventType`:

```proto
  STREAM_EVENT_TYPE_ACTION_PROPOSAL = 15;
```

Add after `rpc GetRunStatus(...)` in `service ChatService`:

```proto
  // Applies a proposal the agent streamed (action_proposal) to its trip.
  // FailedPrecondition when it was already applied or dismissed, has
  // expired, or base_version is stale.
  rpc ApplyTripAction(ApplyTripActionRequest) returns (ApplyTripActionResponse);
  rpc DismissTripAction(DismissTripActionRequest) returns (DismissTripActionResponse);
```

- [ ] **Step 4: Lint, breaking check, generate**

Run: `buf lint && buf breaking --against '.git#branch=origin/main' && buf generate`
Expected: no lint errors, no breaking changes (every change is additive), and new code in `gen/go/loci/chat/`, `gen/swift/…` and `gen/ts/…`.

- [ ] **Step 5: Commit, PR, merge, release**

```bash
git add proto/loci/chat/chat.proto gen/
git commit -m "feat(chat): trip actions the agent proposes and the traveller confirms"
git push -u origin feat/trip-agent-actions && gh pr create --fill
# after merge:
git fetch --tags origin && git tag --sort=-creatordate | head -3      # next free minor, e.g. v5.35.0
git switch --detach origin/main && test -z "$(git status --porcelain)"
make release VERSION=v5.35.0
git show v5.35.0:proto/loci/chat/chat.proto | grep -c "rpc ApplyTripAction"   # expect 1
```

---

### Task 2: Server proto bump

**Files:** `go.mod`, `go.sum`, `gen/`.

- [ ] **Step 1: Bump**

```bash
cd /private/tmp/api-agent-actions && git fetch origin && git rebase origin/main
GOFLAGS=-mod=mod go get github.com/FACorreiaa/loci-connect-proto/v5@v5.35.0 && make generate && go build ./...
```

Expected: the build passes, and `ChatHandler` serves `ApplyTripAction` as Unimplemented through the embedded `UnimplementedChatServiceHandler`.

- [ ] **Step 2: Commit**

```bash
git add go.mod go.sum gen && git commit -m "chore(deps): loci-connect-proto v5.35.0"
```

---

### Task 3: Migration

**Files:**
- Create `pkg/db/migrations/0110_trip_action_proposals.up.sql`. Check `ls pkg/db/migrations | tail -2` and take the next free number.

- [ ] **Step 1: Write it**

```sql
-- +goose Up
-- +goose StatementBegin
-- What the chat agent proposed to change on a trip, waiting for the
-- traveller to confirm. Stored server-side so a proposal is applied at most
-- once, cannot be altered between proposing and applying, and fits in a
-- Telegram button (callback_data is capped at 64 bytes; an id fits, an
-- action does not). Expiry is read at apply time rather than swept.
CREATE TABLE IF NOT EXISTS trip_action_proposals (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    trip_id    UUID NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    session_id UUID NULL,
    action     JSONB NOT NULL,
    options    JSONB NOT NULL DEFAULT '[]',
    summary    TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'applied', 'dismissed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_trip_action_proposals_trip
    ON trip_action_proposals (trip_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS trip_action_proposals;
-- +goose StatementEnd
```

- [ ] **Step 2: Check and commit**

Run: `./scripts/check-migrations.sh`
Expected: `no duplicate versions.`

```bash
git add pkg/db/migrations/0110_trip_action_proposals.up.sql
git commit -m "feat(tripaction): table for proposals the agent makes"
```

---

### Task 4: `trip.Service.ReplaceDays` and exported mappers

**Files:**
- Modify `internal/domain/trip/service.go` and `internal/domain/trip/mappers.go`.
- Test in `internal/domain/trip/service_test.go`.

**Interfaces:**
- Produces:
  - `func (s *Service) ReplaceDays(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, days []TripDay) (*Trip, error)`
  - `func ToProto(t *Trip) *tripv1.TripDraft`
  - `func StayToProto(s TripStay) *tripv1.TripStay`
  - `func FlightToProto(f TripFlight) *tripv1.TripFlight`

- [ ] **Step 1: Make `memTrips.SaveTrip` real**

`memTrips.SaveTrip` currently panics. Replace it in `service_test.go`:

```go
func (m *memTrips) SaveTrip(_ context.Context, t *Trip, base int64) (*Trip, error) {
	if m.trip.Version != base {
		return nil, ErrVersionConflict
	}
	m.saves++
	t.Version = base + 1
	m.trip = t
	return t, nil
}
```

- [ ] **Step 2: Failing tests (append to `service_test.go`)**

```go
func TestService_ReplaceDaysRenumbersAndDatesFromStart(t *testing.T) {
	svc, mem, uid, tid := newServiceFixture()
	start, end := day("2026-11-12"), day("2026-11-17")
	mem.trip.StartDate, mem.trip.EndDate = &start, &end
	mem.trip.Stays = []TripStay{{CityName: "Lisbon", Name: "Hotel Avenida"}}
	oldID := uuid.New()
	got, err := svc.ReplaceDays(context.Background(), uid, tid, 3, []TripDay{
		{ID: oldID, DayNumber: 7, Stops: []TripStop{{ID: uuid.New(), Name: "Belém"}}},
		{DayNumber: 9},
		{DayNumber: 2},
	})
	require.NoError(t, err)
	require.Len(t, got.Days, 3)
	for i, d := range got.Days {
		require.EqualValues(t, i+1, d.DayNumber)
		require.Equal(t, start.AddDate(0, 0, i), *d.Date)
	}
	require.Equal(t, uuid.Nil, got.Days[0].ID, "a re-plan's days are new days")
	require.Equal(t, uuid.Nil, got.Days[0].Stops[0].ID)
	require.Len(t, got.Stays, 1, "stays survive a re-plan")
}

func TestService_ReplaceDaysRejects(t *testing.T) {
	svc, mem, uid, tid := newServiceFixture()
	_, err := svc.ReplaceDays(context.Background(), uid, tid, 2, []TripDay{{}})
	require.ErrorIs(t, err, ErrVersionConflict)
	_, err = svc.ReplaceDays(context.Background(), uid, tid, 3, nil)
	require.ErrorIs(t, err, ErrInvalidEdit)
	require.Zero(t, mem.saves)
}
```

- [ ] **Step 3: Run them to see them fail**

Run: `go test -p 2 ./internal/domain/trip/ -run TestService_ReplaceDays`
Expected: FAIL with `svc.ReplaceDays undefined`.

- [ ] **Step 4: Implement**

Append to `service.go`:

```go
// ReplaceDays swaps a trip's days for a re-planned set (the chat agent's
// "make it 6 days"), keeping its dates, stays and flights. Days are
// renumbered 1..n as new days and, when the trip is dated, dated from its
// start, so the calendar follows the new plan.
func (s *Service) ReplaceDays(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, days []TripDay) (*Trip, error) {
	if len(days) == 0 || len(days) > MaxTripSpanDays {
		return nil, invalid("a plan has 1 to %d days", MaxTripSpanDays)
	}
	t, err := s.trips.GetTrip(ctx, tripID, userID)
	if err != nil {
		return nil, err
	}
	if t.Version != baseVersion {
		return nil, ErrVersionConflict
	}
	t.Days = make([]TripDay, len(days))
	for i, d := range days {
		d.ID = uuid.Nil
		d.DayNumber = int32(i + 1)
		d.Date = nil
		if t.StartDate != nil {
			at := t.StartDate.AddDate(0, 0, i)
			d.Date = &at
		}
		stops := make([]TripStop, len(d.Stops))
		for j, st := range d.Stops {
			st.ID = uuid.Nil
			stops[j] = st
		}
		d.Stops = stops
		t.Days[i] = d
	}
	return s.trips.SaveTrip(ctx, t, baseVersion)
}
```

Append to `mappers.go`:

```go
// ToProto, StayToProto and FlightToProto are the wire forms other domains
// (the chat agent's proposals) return; the trip handler keeps using its own.
func ToProto(t *Trip) *tripv1.TripDraft { return tripToProto(t) }

func StayToProto(s TripStay) *tripv1.TripStay { return stayToProto(s) }

func FlightToProto(f TripFlight) *tripv1.TripFlight { return flightToProto(f) }
```

- [ ] **Step 5: Run the tests, then commit**

Run: `go test -p 2 ./internal/domain/trip/`
Expected: PASS.

```bash
git add internal/domain/trip/service.go internal/domain/trip/mappers.go internal/domain/trip/service_test.go
git commit -m "feat(trip): ReplaceDays for a re-planned itinerary, and exported mappers"
```

---

### Task 5: Actions, validation and star ratings (pure)

**Files:**
- Create `internal/domain/tripaction/action.go` and `internal/domain/tripaction/stars.go`.
- Test in `internal/domain/tripaction/action_test.go`.

**Interfaces:**
- Produces:
  - `type Kind string` with `KindSetDates`, `KindSearchHotels`, `KindRegenerateDays`, `KindSearchFlights`
  - `type Place struct{Name, IATA string}` and `type Action struct{…}` (fields below)
  - `func normalize(a Action, cities []string) (Action, error)`
  - `var cabins map[string]flights.Cabin`
  - `func starsOf(s string) (float64, bool)`
  - `func starsWithin(rating string, lo, hi int) bool`

- [ ] **Step 1: Failing tests**

```go
package tripaction

import (
	"testing"

	"github.com/stretchr/testify/require"
)

var lisbon = []string{"Lisbon"}

func TestNormalize_SetDates(t *testing.T) {
	_, err := normalize(Action{Kind: KindSetDates, StartDate: "2026-11-12", EndDate: "2026-11-17"}, lisbon)
	require.NoError(t, err)
	for _, a := range []Action{
		{Kind: KindSetDates, StartDate: "12 Nov", EndDate: "2026-11-17"},
		{Kind: KindSetDates, StartDate: "2026-11-17", EndDate: "2026-11-12"},
		{Kind: KindSetDates, StartDate: "2026-11-01", EndDate: "2026-12-01"},
	} {
		_, err := normalize(a, lisbon)
		require.Error(t, err, a)
	}
}

func TestNormalize_SearchHotels(t *testing.T) {
	a, err := normalize(Action{Kind: KindSearchHotels, City: " lisbon", MinStars: 4}, lisbon)
	require.NoError(t, err)
	require.Equal(t, "Lisbon", a.City, "the trip's spelling")
	require.Equal(t, 5, a.MaxStars, "4 and up when no upper bound")

	a, err = normalize(Action{Kind: KindSearchHotels}, lisbon)
	require.NoError(t, err)
	require.Equal(t, "Lisbon", a.City, "a one-city trip fills the city in")
	require.Equal(t, 0, a.MinStars)

	_, err = normalize(Action{Kind: KindSearchHotels, City: "Porto", MinStars: 4}, lisbon)
	require.Error(t, err, "not on the trip")
	_, err = normalize(Action{Kind: KindSearchHotels, City: "Lisbon", MinStars: 5, MaxStars: 3}, lisbon)
	require.Error(t, err)
	_, err = normalize(Action{Kind: KindSearchHotels, City: "Lisbon", MinStars: 7}, lisbon)
	require.Error(t, err)
}

func TestNormalize_RegenerateDays(t *testing.T) {
	_, err := normalize(Action{Kind: KindRegenerateDays, Days: 6}, lisbon)
	require.NoError(t, err)
	for _, n := range []int{0, 31, -1} {
		_, err := normalize(Action{Kind: KindRegenerateDays, Days: n}, lisbon)
		require.Error(t, err, n)
	}
}

func TestNormalize_SearchFlights(t *testing.T) {
	a, err := normalize(Action{
		Kind: KindSearchFlights, Origin: Place{Name: "New York", IATA: "jfk"}, Destination: Place{Name: "Lisbon", IATA: "LIS"},
		Depart: "2026-11-12", Cabin: "first-ish",
	}, lisbon)
	require.NoError(t, err)
	require.Empty(t, a.Origin.IATA, "a malformed code is dropped, not fatal")
	require.Equal(t, "LIS", a.Destination.IATA)
	require.Equal(t, 1, a.Passengers)
	require.Empty(t, a.Cabin, "an unknown cabin is left unspecified")

	for _, bad := range []Action{
		{Kind: KindSearchFlights, Destination: Place{Name: "Lisbon"}, Depart: "2026-11-12"},
		{Kind: KindSearchFlights, Origin: Place{Name: "NYC"}, Destination: Place{Name: "Lisbon"}, Depart: "soon"},
		{Kind: KindSearchFlights, Origin: Place{Name: "NYC"}, Destination: Place{Name: "Lisbon"}, Depart: "2026-11-12", Return: "2026-11-01"},
		{Kind: KindSearchFlights, Origin: Place{Name: "NYC"}, Destination: Place{Name: "Lisbon"}, Depart: "2026-11-12", Passengers: 12},
	} {
		_, err := normalize(bad, lisbon)
		require.Error(t, err, bad)
	}
}

func TestNormalize_UnknownKind(t *testing.T) {
	_, err := normalize(Action{Kind: "book_restaurant"}, lisbon)
	require.Error(t, err)
}

func TestStars(t *testing.T) {
	for in, want := range map[string]float64{"4": 4, "4.5": 4.5, "4 stars": 4, "★★★★": 4, " 3 ": 3} {
		got, ok := starsOf(in)
		require.True(t, ok, in)
		require.Equal(t, want, got, in)
	}
	for _, in := range []string{"", "luxury", "9", "0"} {
		_, ok := starsOf(in)
		require.False(t, ok, in)
	}
	require.True(t, starsWithin("4.5", 4, 4), "4.5 counts as a four-star")
	require.False(t, starsWithin("3", 4, 5))
	require.True(t, starsWithin("", 0, 5), "no filter keeps unrated hotels")
	require.False(t, starsWithin("", 4, 5), "a filter drops unrated hotels")
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/domain/tripaction/`
Expected: FAIL to compile with `undefined: normalize`.

- [ ] **Step 3: Implement `action.go`**

```go
// Package tripaction is the chat agent's write path into a trip: it turns a
// message into typed actions, offers them as proposals, and applies the one
// the traveller confirms through trip.Service. The agent proposes; nothing
// changes until the traveller says yes.
package tripaction

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
)

// Kind names what an action changes.
type Kind string

const (
	KindSetDates       Kind = "set_dates"
	KindSearchHotels   Kind = "search_hotels"
	KindRegenerateDays Kind = "regenerate_days"
	KindSearchFlights  Kind = "search_flights"
)

// Place is one end of a flight as the extractor names it.
type Place struct {
	Name string `json:"name"`
	IATA string `json:"iata,omitempty"`
}

// Action is one change the agent proposes. Only the fields its Kind uses
// are set. It is also the JSON shape the extractor is asked for.
type Action struct {
	Kind Kind `json:"kind"`

	StartDate string `json:"start_date,omitempty"` // set_dates
	EndDate   string `json:"end_date,omitempty"`

	City     string `json:"city,omitempty"` // search_hotels
	MinStars int    `json:"min_stars,omitempty"`
	MaxStars int    `json:"max_stars,omitempty"`

	Days int `json:"days,omitempty"` // regenerate_days

	Origin      Place  `json:"origin,omitzero"` // search_flights
	Destination Place  `json:"destination,omitzero"`
	Depart      string `json:"depart,omitempty"`
	Return      string `json:"return,omitempty"`
	Passengers  int    `json:"passengers,omitempty"`
	Cabin       string `json:"cabin,omitempty"`
}

var errInvalid = errors.New("invalid trip action")

func bad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errInvalid, fmt.Sprintf(format, a...))
}

// cabins are the cabin words the extractor may use.
var cabins = map[string]flights.Cabin{
	"economy":         flights.CabinEconomy,
	"premium_economy": flights.CabinPremiumEconomy,
	"business":        flights.CabinBusiness,
	"first":           flights.CabinFirst,
}

const maxPassengers = 9

// normalize checks an extracted action against the trip and fills what the
// trip makes obvious. An action that fails is dropped, never applied: the
// model's output is a suggestion, and the rules here are the same ones
// trip.Service enforces when the traveller confirms.
func normalize(a Action, cities []string) (Action, error) {
	switch a.Kind {
	case KindSetDates:
		start, err := time.Parse(time.DateOnly, a.StartDate)
		if err != nil {
			return a, bad("start date %q", a.StartDate)
		}
		end, err := time.Parse(time.DateOnly, a.EndDate)
		if err != nil {
			return a, bad("end date %q", a.EndDate)
		}
		if end.Before(start) {
			return a, bad("end date before start date")
		}
		if span := int(end.Sub(start).Hours()/24) + 1; span > trip.MaxTripSpanDays {
			return a, bad("%d days is longer than a trip", span)
		}
	case KindSearchHotels:
		city, ok := pickCity(a.City, cities)
		if !ok {
			return a, bad("%q is not a city on this trip", a.City)
		}
		a.City = city
		if a.MinStars < 0 || a.MinStars > 5 || a.MaxStars < 0 || a.MaxStars > 5 {
			return a, bad("stars run from 1 to 5")
		}
		if a.MaxStars == 0 {
			a.MaxStars = 5
		}
		if a.MinStars > a.MaxStars {
			return a, bad("min stars above max stars")
		}
	case KindRegenerateDays:
		if a.Days < 1 || a.Days > trip.MaxTripSpanDays {
			return a, bad("a plan has 1 to %d days, not %d", trip.MaxTripSpanDays, a.Days)
		}
	case KindSearchFlights:
		a.Origin.Name = strings.TrimSpace(a.Origin.Name)
		a.Destination.Name = strings.TrimSpace(a.Destination.Name)
		if a.Origin.Name == "" || a.Destination.Name == "" {
			return a, bad("a flight needs an origin and a destination")
		}
		// A wrong code is dropped rather than fatal: Google Flights takes the
		// name, and Skyscanner links are simply not built without codes.
		if !flights.ValidIATA(a.Origin.IATA) {
			a.Origin.IATA = ""
		}
		if !flights.ValidIATA(a.Destination.IATA) {
			a.Destination.IATA = ""
		}
		depart, err := time.Parse(time.DateOnly, a.Depart)
		if err != nil {
			return a, bad("departure date %q", a.Depart)
		}
		if a.Return != "" {
			ret, err := time.Parse(time.DateOnly, a.Return)
			if err != nil || ret.Before(depart) {
				return a, bad("return date %q", a.Return)
			}
		}
		if a.Passengers < 1 {
			a.Passengers = 1
		}
		if a.Passengers > maxPassengers {
			return a, bad("at most %d passengers", maxPassengers)
		}
		if _, ok := cabins[a.Cabin]; !ok {
			a.Cabin = ""
		}
	default:
		return a, bad("unknown kind %q", a.Kind)
	}
	return a, nil
}

// pickCity is the trip's spelling of name. An empty name on a one-city trip
// means that city.
func pickCity(name string, cities []string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" && len(cities) == 1 {
		return cities[0], true
	}
	for _, c := range cities {
		if strings.EqualFold(strings.TrimSpace(c), name) {
			return c, true
		}
	}
	return "", false
}
```

- [ ] **Step 4: Implement `stars.go`**

```go
package tripaction

import (
	"math"
	"strconv"
	"strings"
)

// starsOf reads a hotel's star rating as stored: "4", "4.5", "4 stars" or
// "★★★★". ok is false when it says nothing usable.
func starsOf(s string) (float64, bool) {
	if n := strings.Count(s, "★"); n > 0 {
		return float64(n), n <= 5
	}
	s = strings.TrimSpace(s)
	end := 0
	for end < len(s) && (s[end] >= '0' && s[end] <= '9' || s[end] == '.') {
		end++
	}
	if end == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(s[:end], 64)
	if err != nil || v <= 0 || v > 5 {
		return 0, false
	}
	return v, true
}

// starsWithin keeps a hotel whose whole stars fall in lo..hi. A 4.5 is a
// four-star. 0..5 is no filter at all, and keeps hotels with no rating; any
// narrower range drops them, since nobody asking for four stars wants a guess.
func starsWithin(rating string, lo, hi int) bool {
	if lo == 0 && hi == 5 {
		return true
	}
	v, ok := starsOf(rating)
	if !ok {
		return false
	}
	whole := int(math.Floor(v))
	return whole >= lo && whole <= hi
}
```

- [ ] **Step 5: Run the tests, then commit**

Run: `go test ./internal/domain/tripaction/`
Expected: PASS.

```bash
git add internal/domain/tripaction/action.go internal/domain/tripaction/stars.go internal/domain/tripaction/action_test.go
git commit -m "feat(tripaction): action types, validation and star ratings"
```

---

### Task 6: Extraction

**Files:**
- Create `internal/domain/tripaction/extract.go`.
- Test in `internal/domain/tripaction/extract_test.go`.

**Interfaces:**
- Consumes: `normalize` (Task 5).
- Produces:
  - `type Generator interface{ GenerateText(ctx context.Context, prompt string) (string, error) }`
  - `type Snapshot struct{ Cities []string; StartDate, EndDate string; Days int; Today time.Time }`
  - `func Extract(ctx context.Context, gen Generator, message string, snap Snapshot) ([]Action, error)`
  - `const maxActions = 4`

- [ ] **Step 1: Failing tests**

```go
package tripaction

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// scripted returns replies in order and records the prompts it was given.
type scripted struct {
	replies []string
	err     error
	prompts []string
}

func (s *scripted) GenerateText(_ context.Context, prompt string) (string, error) {
	s.prompts = append(s.prompts, prompt)
	if s.err != nil {
		return "", s.err
	}
	r := s.replies[0]
	s.replies = s.replies[1:]
	return r, nil
}

var snap = Snapshot{Cities: []string{"Lisbon"}, Days: 3, Today: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}

func TestExtract_ParsesFencedJSONAndDropsInvalid(t *testing.T) {
	gen := &scripted{replies: []string{"Sure!\n```json\n" + `{"actions":[
		{"kind":"set_dates","start_date":"2026-11-12","end_date":"2026-11-17"},
		{"kind":"search_hotels","city":"Lisbon","min_stars":4,"max_stars":4},
		{"kind":"search_hotels","city":"Paris","min_stars":4},
		{"kind":"teleport"}]}` + "\n```"}}
	got, err := Extract(context.Background(), gen, "4-star hotels, 12 to 17 Nov", snap)
	require.NoError(t, err)
	require.Len(t, got, 2, "Paris is not on the trip; teleport is not an action")
	require.Equal(t, KindSetDates, got[0].Kind)
	require.Equal(t, KindSearchHotels, got[1].Kind)
}

func TestExtract_TellsTheModelAboutTheTrip(t *testing.T) {
	gen := &scripted{replies: []string{`{"actions":[]}`}}
	_, err := Extract(context.Background(), gen, "make it longer", snap)
	require.NoError(t, err)
	p := gen.prompts[0]
	require.Contains(t, p, "Lisbon")
	require.Contains(t, p, "2026-10-01")
	require.Contains(t, p, "make it longer")
}

func TestExtract_RetriesOnceThenGivesUp(t *testing.T) {
	gen := &scripted{replies: []string{"I can't do JSON today", `{"actions":[{"kind":"regenerate_days","days":6}]}`}}
	got, err := Extract(context.Background(), gen, "make it 6 days", snap)
	require.NoError(t, err)
	require.Len(t, got, 1)

	gen = &scripted{replies: []string{"nope", "still nope"}}
	_, err = Extract(context.Background(), gen, "make it 6 days", snap)
	require.Error(t, err)
	require.Len(t, gen.prompts, 2, "one retry, not more")
}

func TestExtract_ModelDown(t *testing.T) {
	_, err := Extract(context.Background(), &scripted{err: errors.New("402 out of credits")}, "x", snap)
	require.Error(t, err)
}

func TestExtract_CapsActions(t *testing.T) {
	one := `{"kind":"regenerate_days","days":2}`
	gen := &scripted{replies: []string{`{"actions":[` + strings.Repeat(one+",", 6) + one + `]}`}}
	got, err := Extract(context.Background(), gen, "x", snap)
	require.NoError(t, err)
	require.Len(t, got, maxActions)
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/domain/tripaction/ -run TestExtract`
Expected: FAIL to compile with `undefined: Extract`.

- [ ] **Step 3: Implement**

```go
package tripaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Generator is the one LLM call extraction makes. The chat service supplies
// it, on its own model chain and LLM slots.
type Generator interface {
	GenerateText(ctx context.Context, prompt string) (string, error)
}

// Snapshot is what extraction is told about the trip the message is about.
type Snapshot struct {
	Cities    []string
	StartDate string
	EndDate   string
	Days      int
	Today     time.Time
}

// maxActions caps one message's cards; a reply listing more is a model
// rambling, not a traveller asking.
const maxActions = 4

const extractPrompt = `You turn a traveller's chat message into changes to the trip they are editing.
Reply with JSON only: {"actions": [...]}. An empty list means the message asks for no change.

The trip: cities %s; dates %s; %d days planned. Today is %s; a date without a year is its next occurrence.

Action kinds. Use only these, and only for what the message asks:
- {"kind":"set_dates","start_date":"YYYY-MM-DD","end_date":"YYYY-MM-DD"}  ("5 days from 12 Nov" ends 16 Nov)
- {"kind":"search_hotels","city":"<a city of the trip>","min_stars":N,"max_stars":M}  ("4-star" is 4..4; "4 stars or more" is 4..5; no stars mentioned is 0..0)
- {"kind":"regenerate_days","days":N}  (re-plan the itinerary as N days)
- {"kind":"search_flights","origin":{"name":"...","iata":"XXX"},"destination":{"name":"...","iata":"XXX"},"depart":"YYYY-MM-DD","return":"YYYY-MM-DD","passengers":N,"cabin":"economy|premium_economy|business|first"}  (omit return for one way; omit iata when unsure)

Example message: "4-star hotels in Lisbon from 12 to 17 November, and make it 6 days"
Example reply: {"actions":[{"kind":"set_dates","start_date":"2026-11-12","end_date":"2026-11-17"},{"kind":"search_hotels","city":"Lisbon","min_stars":4,"max_stars":4},{"kind":"regenerate_days","days":6}]}

Message: %s`

// Extract asks the model which changes message wants, retrying once when the
// reply is not JSON, and keeps only actions that pass normalize. An error
// means the model could not be asked or never answered in JSON; the caller
// answers the message as usual.
func Extract(ctx context.Context, gen Generator, message string, snap Snapshot) ([]Action, error) {
	dates := "not set"
	if snap.StartDate != "" {
		dates = snap.StartDate + " to " + snap.EndDate
	}
	prompt := fmt.Sprintf(extractPrompt, strings.Join(snap.Cities, ", "), dates, snap.Days,
		snap.Today.Format(time.DateOnly), message)

	var (
		raw []Action
		err error
	)
	for attempt := 0; attempt < 2; attempt++ {
		var text string
		if text, err = gen.GenerateText(ctx, prompt); err != nil {
			return nil, fmt.Errorf("extract trip actions: %w", err)
		}
		if raw, err = parseActions(text); err == nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}

	out := make([]Action, 0, len(raw))
	for _, a := range raw {
		if n, verr := normalize(a, snap.Cities); verr == nil {
			out = append(out, n)
		}
		if len(out) == maxActions {
			break
		}
	}
	return out, nil
}

// parseActions reads the first JSON object in text, fenced or not.
func parseActions(text string) ([]Action, error) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return nil, errors.New("no JSON object in the reply")
	}
	var doc struct {
		Actions []Action `json:"actions"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &doc); err != nil {
		return nil, fmt.Errorf("parse trip actions: %w", err)
	}
	return doc.Actions, nil
}
```

- [ ] **Step 4: Run the tests, then commit**

Run: `go test ./internal/domain/tripaction/`
Expected: PASS.

```bash
git add internal/domain/tripaction/extract.go internal/domain/tripaction/extract_test.go
git commit -m "feat(tripaction): extract trip actions from a message with one JSON call"
```

---

### Task 7: Proposal store

**Files:**
- Create `internal/domain/tripaction/store.go`.
- Test in `internal/domain/tripaction/store_integration_test.go` (`//go:build integration`).

**Interfaces:**
- Produces:
  - `type Status string` with `StatusPending`, `StatusApplied`, `StatusDismissed`
  - `type Option struct{ Label, Detail string; Stay *trip.TripStay; Flight *trip.TripFlight }`
  - `type Proposal struct{ ID, UserID, TripID, SessionID uuid.UUID; Action Action; Summary string; Options []Option; Status Status; CreatedAt, ExpiresAt time.Time }`
  - `type Store interface{ Create(ctx, *Proposal) error; Get(ctx, id, userID uuid.UUID) (*Proposal, error); Transition(ctx, id uuid.UUID, from, to Status) error }`
  - `func NewStore(db *pgxpool.Pool) Store`
  - `var ErrNotFound, ErrNotPending, ErrExpired, ErrNoOption`

- [ ] **Step 1: Failing integration test**

```go
//go:build integration

package tripaction

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
)

var testDB *pgxpool.Pool

func TestMain(m *testing.M) {
	testDB = testsupport.MustPool()
	os.Exit(m.Run())
}

func newUserAndTrip(t *testing.T) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	uid := uuid.New()
	_, err := testDB.Exec(ctx, `INSERT INTO users (id, email) VALUES ($1, $2)`, uid, "ta-"+uid.String()+"@loci.test")
	require.NoError(t, err)
	var tid uuid.UUID
	require.NoError(t, testDB.QueryRow(ctx,
		`INSERT INTO trips (user_id, city_name, title, constraints, version) VALUES ($1, 'Lisbon', 'Lisbon', '{}', 1) RETURNING id`,
		uid).Scan(&tid))
	return uid, tid
}

func TestStore_RoundTripAndOneShotTransitions(t *testing.T) {
	ctx := context.Background()
	store := NewStore(testDB)
	uid, tid := newUserAndTrip(t)

	p := &Proposal{
		UserID: uid, TripID: tid,
		Action:  Action{Kind: KindSearchHotels, City: "Lisbon", MinStars: 4, MaxStars: 4},
		Summary: "4★ hotels in Lisbon",
		Options: []Option{{Label: "Hotel Avenida · 4★", Stay: &trip.TripStay{CityName: "Lisbon", Name: "Hotel Avenida", StarRating: "4"}}},
		ExpiresAt: time.Now().Add(time.Hour),
	}
	require.NoError(t, store.Create(ctx, p))
	require.NotEqual(t, uuid.Nil, p.ID)
	require.Equal(t, StatusPending, p.Status)

	got, err := store.Get(ctx, p.ID, uid)
	require.NoError(t, err)
	require.Equal(t, p.Action, got.Action)
	require.Equal(t, "Hotel Avenida", got.Options[0].Stay.Name)
	require.Equal(t, uuid.Nil, got.SessionID, "no session stored as NULL, read back as Nil")

	_, err = store.Get(ctx, p.ID, uuid.New())
	require.ErrorIs(t, err, ErrNotFound, "someone else's proposal")

	require.NoError(t, store.Transition(ctx, p.ID, StatusPending, StatusApplied))
	require.ErrorIs(t, store.Transition(ctx, p.ID, StatusPending, StatusApplied), ErrNotPending, "applied once")
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `DOCKER_HOST=… go test -tags=integration -p 1 -count=1 ./internal/domain/tripaction/` (with the env from Global Constraints)
Expected: FAIL to compile with `undefined: NewStore`.

- [ ] **Step 3: Implement**

```go
package tripaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
)

var (
	// ErrNotFound: no such proposal, or not the caller's.
	ErrNotFound = errors.New("trip action not found")
	// ErrNotPending: already applied or dismissed.
	ErrNotPending = errors.New("trip action was already applied or dismissed")
	// ErrExpired: older than proposalTTL; the trip has likely moved on.
	ErrExpired = errors.New("trip action has expired")
	// ErrNoOption: a pick-one action applied without a valid option.
	ErrNoOption = errors.New("pick one of the action's options")
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusApplied   Status = "applied"
	StatusDismissed Status = "dismissed"
)

// Option is one choice of a pick-one action.
type Option struct {
	Label  string           `json:"label"`
	Detail string           `json:"detail,omitempty"`
	Stay   *trip.TripStay   `json:"stay,omitempty"`
	Flight *trip.TripFlight `json:"flight,omitempty"`
}

// Proposal is an action waiting for the traveller. SessionID is uuid.Nil
// when the turn had no chat thread to confirm into.
type Proposal struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	TripID    uuid.UUID `json:"trip_id"`
	SessionID uuid.UUID `json:"session_id"`
	Action    Action    `json:"action"`
	Summary   string    `json:"summary"`
	Options   []Option  `json:"options"`
	Status    Status    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Store keeps proposals. Transition is the one-shot guard: it moves a
// proposal from one status to another only if it is still in the first.
type Store interface {
	Create(ctx context.Context, p *Proposal) error
	Get(ctx context.Context, id, userID uuid.UUID) (*Proposal, error)
	Transition(ctx context.Context, id uuid.UUID, from, to Status) error
}

type pgStore struct{ db *pgxpool.Pool }

func NewStore(db *pgxpool.Pool) Store { return &pgStore{db: db} }

func nullable(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func (s *pgStore) Create(ctx context.Context, p *Proposal) error {
	action, err := json.Marshal(p.Action)
	if err != nil {
		return fmt.Errorf("marshal action: %w", err)
	}
	options := []byte("[]")
	if len(p.Options) > 0 {
		if options, err = json.Marshal(p.Options); err != nil {
			return fmt.Errorf("marshal options: %w", err)
		}
	}
	p.Status = StatusPending
	return s.db.QueryRow(ctx, `
		INSERT INTO trip_action_proposals (user_id, trip_id, session_id, action, options, summary, status, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at`,
		p.UserID, p.TripID, nullable(p.SessionID), action, options, p.Summary, p.Status, p.ExpiresAt).
		Scan(&p.ID, &p.CreatedAt)
}

func (s *pgStore) Get(ctx context.Context, id, userID uuid.UUID) (*Proposal, error) {
	var (
		p                 Proposal
		session           *uuid.UUID
		action, options   []byte
	)
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, trip_id, session_id, action, options, summary, status, created_at, expires_at
		FROM trip_action_proposals WHERE id = $1 AND user_id = $2`, id, userID).
		Scan(&p.ID, &p.UserID, &p.TripID, &session, &action, &options, &p.Summary, &p.Status, &p.CreatedAt, &p.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get trip action: %w", err)
	}
	if session != nil {
		p.SessionID = *session
	}
	if err := json.Unmarshal(action, &p.Action); err != nil {
		return nil, fmt.Errorf("unmarshal action: %w", err)
	}
	if err := json.Unmarshal(options, &p.Options); err != nil {
		return nil, fmt.Errorf("unmarshal options: %w", err)
	}
	return &p, nil
}

func (s *pgStore) Transition(ctx context.Context, id uuid.UUID, from, to Status) error {
	ct, err := s.db.Exec(ctx, `UPDATE trip_action_proposals SET status = $3 WHERE id = $1 AND status = $2`, id, from, to)
	if err != nil {
		return fmt.Errorf("trip action %s→%s: %w", from, to, err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotPending
	}
	return nil
}
```

- [ ] **Step 4: Run it, then commit**

Run: the integration command from Step 2.
Expected: PASS.

```bash
git add internal/domain/tripaction/store.go internal/domain/tripaction/store_integration_test.go
git commit -m "feat(tripaction): Postgres store for proposals, applied at most once"
```

---

### Task 8: `tripaction.Service` (Propose, Apply, Dismiss)

**Files:**
- Create `internal/domain/tripaction/service.go`.
- Test in `internal/domain/tripaction/service_test.go`.

**Interfaces:**
- Consumes: `Extract` (Task 6), `Store` (Task 7), and `trip.Service.SetDates`/`SetStay`/`AddFlight`/`ReplaceDays`/`FlightLinks` (plan 1 plus Task 4).
- Produces:
  - `type Deps struct{ LLM Generator; Store Store; Trips Trips; Plans Plans; Hotels Hotels; Places geocode.Forward; Regen Regenerator; Sessions Sessions; Logger *slog.Logger; Now func() time.Time }`
  - `func NewService(d Deps) *Service`
  - `func (s *Service) Propose(ctx context.Context, userID, tripID, sessionID uuid.UUID, message string) ([]Proposal, error)`
  - `func (s *Service) Apply(ctx context.Context, userID, proposalID uuid.UUID, option *int, baseVersion int64) (*trip.Trip, *locitypes.ConversationMessage, error)`
  - `func (s *Service) Dismiss(ctx context.Context, userID, proposalID uuid.UUID) error`
  - `type Regenerator interface{ GenerateDays(ctx context.Context, userID uuid.UUID, cityName string, days int) ([]trip.TripDay, error) }`

- [ ] **Step 1: Failing tests**

```go
package tripaction

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
	"github.com/FACorreiaa/loci-connect-api/pkg/geocode"
)

// --- fakes ---

type memStore struct{ byID map[uuid.UUID]*Proposal }

func (m *memStore) Create(_ context.Context, p *Proposal) error {
	p.ID, p.Status, p.CreatedAt = uuid.New(), StatusPending, time.Now()
	cp := *p
	m.byID[p.ID] = &cp
	return nil
}

func (m *memStore) Get(_ context.Context, id, userID uuid.UUID) (*Proposal, error) {
	p, ok := m.byID[id]
	if !ok || p.UserID != userID {
		return nil, ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (m *memStore) Transition(_ context.Context, id uuid.UUID, from, to Status) error {
	p, ok := m.byID[id]
	if !ok || p.Status != from {
		return ErrNotPending
	}
	p.Status = to
	return nil
}

type fakeTrips struct{ t *trip.Trip }

func (f *fakeTrips) GetTrip(_ context.Context, id, userID uuid.UUID) (*trip.Trip, error) {
	if f.t.ID != id || f.t.UserID != userID {
		return nil, trip.ErrNotFound
	}
	cp := *f.t
	return &cp, nil
}

// fakePlans records what was applied; version moves like the real service.
type fakePlans struct {
	trips   *fakeTrips
	applied []string
	fail    error
}

func (f *fakePlans) bump(base int64, what string) (*trip.Trip, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	if f.trips.t.Version != base {
		return nil, trip.ErrVersionConflict
	}
	f.trips.t.Version++
	f.applied = append(f.applied, what)
	cp := *f.trips.t
	return &cp, nil
}

func (f *fakePlans) SetDates(_ context.Context, _, _ uuid.UUID, base int64, _, _ time.Time) (*trip.Trip, error) {
	return f.bump(base, "dates")
}

func (f *fakePlans) SetStay(_ context.Context, _, _ uuid.UUID, base int64, s trip.TripStay) (*trip.Trip, error) {
	return f.bump(base, "stay:"+s.Name)
}

func (f *fakePlans) AddFlight(_ context.Context, _, _ uuid.UUID, base int64, _ trip.TripFlight) (*trip.Trip, error) {
	return f.bump(base, "flight")
}

func (f *fakePlans) ReplaceDays(_ context.Context, _, _ uuid.UUID, base int64, days []trip.TripDay) (*trip.Trip, error) {
	return f.bump(base, "days")
}

func (f *fakePlans) FlightLinks(q flights.Query) ([]flights.Link, error) { return flights.DeepLinks{}.Links(q), nil }

type fakeHotels struct{ found []locitypes.POIDetailedInfo }

func (f fakeHotels) GetNearbyHotels(context.Context, uuid.UUID, float64, float64, float64, string, string) ([]locitypes.POIDetailedInfo, error) {
	return f.found, nil
}

type fakePlaces struct{}

func (fakePlaces) Search(context.Context, string, int) ([]geocode.Place, error) {
	return []geocode.Place{{Lat: 38.72, Lon: -9.14}}, nil
}

type fakeRegen struct {
	calls int
	err   error
}

func (f *fakeRegen) GenerateDays(context.Context, uuid.UUID, string, int) ([]trip.TripDay, error) {
	f.calls++
	return []trip.TripDay{{DayNumber: 1}}, f.err
}

type fakeSessions struct {
	owner  uuid.UUID
	posted []string
}

func (f *fakeSessions) GetSession(_ context.Context, id uuid.UUID) (*locitypes.ChatSession, error) {
	return &locitypes.ChatSession{ID: id, UserID: f.owner}, nil
}

func (f *fakeSessions) AddMessageToSession(_ context.Context, _ uuid.UUID, m locitypes.ConversationMessage) error {
	f.posted = append(f.posted, m.Content)
	return nil
}

type fixture struct {
	svc      *Service
	store    *memStore
	plans    *fakePlans
	regen    *fakeRegen
	sessions *fakeSessions
	uid, tid uuid.UUID
	now      time.Time
}

func newFixture(t *testing.T, reply string, hotels []locitypes.POIDetailedInfo) *fixture {
	t.Helper()
	uid, tid := uuid.New(), uuid.New()
	trips := &fakeTrips{t: &trip.Trip{ID: tid, UserID: uid, CityName: "Lisbon", Version: 3, Days: make([]trip.TripDay, 3)}}
	f := &fixture{
		store: &memStore{byID: map[uuid.UUID]*Proposal{}}, plans: &fakePlans{trips: trips},
		regen: &fakeRegen{}, sessions: &fakeSessions{owner: uid}, uid: uid, tid: tid,
		now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
	}
	f.svc = NewService(Deps{
		LLM: &scripted{replies: []string{reply, reply}}, Store: f.store, Trips: trips, Plans: f.plans,
		Hotels: fakeHotels{found: hotels}, Places: fakePlaces{}, Regen: f.regen, Sessions: f.sessions,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return f.now },
	})
	return f
}

const allFour = `{"actions":[
 {"kind":"set_dates","start_date":"2026-11-12","end_date":"2026-11-17"},
 {"kind":"search_hotels","city":"Lisbon","min_stars":4,"max_stars":4},
 {"kind":"regenerate_days","days":6},
 {"kind":"search_flights","origin":{"name":"New York","iata":"JFK"},"destination":{"name":"Lisbon","iata":"LIS"},"depart":"2026-11-12","return":"2026-11-17","passengers":2}]}`

var lisbonHotels = []locitypes.POIDetailedInfo{
	{ID: uuid.New(), Name: "Pestana Palace", StarRating: "5", Rating: 4.8, Website: "https://pestana.example"},
	{ID: uuid.New(), Name: "Hotel Avenida", StarRating: "4 stars", Rating: 4.1, Website: "http://avenida.example"},
	{Name: "Generated Inn", StarRating: "4.5", Rating: 4.6},
	{ID: uuid.New(), Name: "No Stars", Rating: 4.9},
}

// --- Propose ---

func TestPropose_OneProposalPerActionWithOptions(t *testing.T) {
	f := newFixture(t, allFour, lisbonHotels)
	sid := uuid.New()
	got, err := f.svc.Propose(context.Background(), f.uid, f.tid, sid, "the lot")
	require.NoError(t, err)
	require.Len(t, got, 4)
	require.Len(t, f.store.byID, 4, "every proposal is stored")
	require.Equal(t, sid, got[0].SessionID)
	require.Equal(t, f.now.Add(proposalTTL), got[0].ExpiresAt)

	hotels := got[1]
	require.Equal(t, KindSearchHotels, hotels.Action.Kind)
	require.Len(t, hotels.Options, 2, "4..4 keeps the 4.5 and the 4; drops the 5 and the unrated")
	require.Equal(t, "Generated Inn", hotels.Options[0].Stay.Name, "best rated first")
	require.Empty(t, hotels.Options[0].Stay.POIID, "a generated hotel has no POI id")
	require.Equal(t, "4.5", hotels.Options[0].Stay.StarRating)
	require.Nil(t, hotels.Options[1].Stay.BookingURL, "an http site is not a booking link")

	flight := got[3]
	require.Len(t, flight.Options, 1)
	require.NotEmpty(t, flight.Options[0].Flight.Links)
	require.EqualValues(t, 2, flight.Options[0].Flight.Passengers)
}

func TestPropose_NothingAskedIsNoProposals(t *testing.T) {
	f := newFixture(t, `{"actions":[]}`, nil)
	got, err := f.svc.Propose(context.Background(), f.uid, f.tid, uuid.Nil, "what's good for dinner?")
	require.NoError(t, err)
	require.Empty(t, got)
	require.Empty(t, f.store.byID)
}

func TestPropose_SomeoneElsesTripOrSession(t *testing.T) {
	f := newFixture(t, allFour, nil)
	_, err := f.svc.Propose(context.Background(), uuid.New(), f.tid, uuid.Nil, "x")
	require.ErrorIs(t, err, trip.ErrNotFound)

	f.sessions.owner = uuid.New()
	got, err := f.svc.Propose(context.Background(), f.uid, f.tid, uuid.New(), "x")
	require.NoError(t, err)
	require.Equal(t, uuid.Nil, got[0].SessionID, "a session the caller does not own is not used")
}

func TestPropose_NoHotelsFound(t *testing.T) {
	f := newFixture(t, `{"actions":[{"kind":"search_hotels","city":"Lisbon","min_stars":5,"max_stars":5}]}`, lisbonHotels[1:])
	got, err := f.svc.Propose(context.Background(), f.uid, f.tid, uuid.Nil, "5 stars")
	require.NoError(t, err)
	require.Empty(t, got[0].Options)
	require.Contains(t, got[0].Summary, "No 5★ hotels")
}

// --- Apply / Dismiss ---

func proposeAll(t *testing.T, f *fixture) []Proposal {
	t.Helper()
	got, err := f.svc.Propose(context.Background(), f.uid, f.tid, uuid.New(), "the lot")
	require.NoError(t, err)
	return got
}

func intp(i int) *int { return &i }

func TestApply_EachKind(t *testing.T) {
	f := newFixture(t, allFour, lisbonHotels)
	ps := proposeAll(t, f)
	ctx := context.Background()

	tr, msg, err := f.svc.Apply(ctx, f.uid, ps[0].ID, nil, 3)
	require.NoError(t, err)
	require.NotNil(t, msg)
	_, _, err = f.svc.Apply(ctx, f.uid, ps[1].ID, intp(1), tr.Version)
	require.NoError(t, err)
	_, _, err = f.svc.Apply(ctx, f.uid, ps[2].ID, nil, 5)
	require.NoError(t, err)
	_, _, err = f.svc.Apply(ctx, f.uid, ps[3].ID, nil, 6)
	require.NoError(t, err)

	require.Equal(t, []string{"dates", "stay:Hotel Avenida", "days", "flight"}, f.plans.applied)
	require.Len(t, f.sessions.posted, 4, "each confirmation lands in the thread")
}

func TestApply_OnlyOnce(t *testing.T) {
	f := newFixture(t, allFour, lisbonHotels)
	ps := proposeAll(t, f)
	ctx := context.Background()
	_, _, err := f.svc.Apply(ctx, f.uid, ps[0].ID, nil, 3)
	require.NoError(t, err)
	_, _, err = f.svc.Apply(ctx, f.uid, ps[0].ID, nil, 4)
	require.ErrorIs(t, err, ErrNotPending)

	require.NoError(t, f.svc.Dismiss(ctx, f.uid, ps[1].ID))
	_, _, err = f.svc.Apply(ctx, f.uid, ps[1].ID, intp(0), 4)
	require.ErrorIs(t, err, ErrNotPending, "dismissed stays dismissed")
	require.Equal(t, []string{"dates"}, f.plans.applied)
}

func TestApply_Refusals(t *testing.T) {
	f := newFixture(t, allFour, lisbonHotels)
	ps := proposeAll(t, f)
	ctx := context.Background()

	_, _, err := f.svc.Apply(ctx, uuid.New(), ps[0].ID, nil, 3)
	require.ErrorIs(t, err, ErrNotFound, "someone else's proposal")

	_, _, err = f.svc.Apply(ctx, f.uid, ps[1].ID, nil, 3)
	require.ErrorIs(t, err, ErrNoOption, "hotels need a pick")
	_, _, err = f.svc.Apply(ctx, f.uid, ps[1].ID, intp(9), 3)
	require.ErrorIs(t, err, ErrNoOption)

	f.now = f.now.Add(25 * time.Hour)
	_, _, err = f.svc.Apply(ctx, f.uid, ps[0].ID, nil, 3)
	require.ErrorIs(t, err, ErrExpired)
	require.Empty(t, f.plans.applied)
}

func TestApply_StaleRePlanNeverGenerates(t *testing.T) {
	f := newFixture(t, allFour, nil)
	ps := proposeAll(t, f)
	_, _, err := f.svc.Apply(context.Background(), f.uid, ps[2].ID, nil, 2)
	require.ErrorIs(t, err, trip.ErrVersionConflict)
	require.Zero(t, f.regen.calls, "the slow generation never runs on a stale trip")
	_, _, err = f.svc.Apply(context.Background(), f.uid, ps[2].ID, nil, 3)
	require.NoError(t, err, "the refusal gave the proposal back")
}

func TestApply_FailureGivesTheProposalBack(t *testing.T) {
	f := newFixture(t, allFour, nil)
	ps := proposeAll(t, f)
	f.regen.err = errors.New("model down")
	_, _, err := f.svc.Apply(context.Background(), f.uid, ps[2].ID, nil, 3)
	require.Error(t, err)
	require.Equal(t, StatusPending, f.store.byID[ps[2].ID].Status)
}

func TestApply_MultiCityRePlanIsRefused(t *testing.T) {
	f := newFixture(t, `{"actions":[{"kind":"regenerate_days","days":4}]}`, nil)
	f.plans.trips.t.Cities = []trip.TripCity{{CityName: "Lisbon"}, {CityName: "Porto"}}
	ps := proposeAll(t, f)
	_, _, err := f.svc.Apply(context.Background(), f.uid, ps[0].ID, nil, 3)
	require.ErrorIs(t, err, trip.ErrInvalidEdit)
	require.Zero(t, f.regen.calls)
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/domain/tripaction/ -run 'TestPropose|TestApply'`
Expected: FAIL to compile with `undefined: NewService`.

- [ ] **Step 3: Implement**

```go
package tripaction

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
	"github.com/FACorreiaa/loci-connect-api/pkg/geocode"
)

const (
	// proposalTTL: after a day the trip has likely moved on, and a card from
	// yesterday's chat should not quietly rewrite today's plan.
	proposalTTL       = 24 * time.Hour
	hotelRadiusMeters = 5000
	maxHotelOptions   = 5
	sourceLabel       = "Trip planner"
)

// Trips reads the trip a proposal is about (trip.Repository).
type Trips interface {
	GetTrip(ctx context.Context, id, userID uuid.UUID) (*trip.Trip, error)
}

// Plans is the write path a confirmed proposal goes through (*trip.Service).
type Plans interface {
	SetDates(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, start, end time.Time) (*trip.Trip, error)
	SetStay(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, stay trip.TripStay) (*trip.Trip, error)
	AddFlight(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, f trip.TripFlight) (*trip.Trip, error)
	ReplaceDays(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, days []trip.TripDay) (*trip.Trip, error)
	FlightLinks(q flights.Query) ([]flights.Link, error)
}

// Hotels is the nearby-hotel search (poi.Service).
type Hotels interface {
	GetNearbyHotels(ctx context.Context, userID uuid.UUID, lat, lon, distance float64, starRating, amenities string) ([]locitypes.POIDetailedInfo, error)
}

// Regenerator plans n days for a city without saving a trip (the chat service).
type Regenerator interface {
	GenerateDays(ctx context.Context, userID uuid.UUID, cityName string, days int) ([]trip.TripDay, error)
}

// Sessions is the chat thread a confirmation is posted into.
type Sessions interface {
	GetSession(ctx context.Context, sessionID uuid.UUID) (*locitypes.ChatSession, error)
	AddMessageToSession(ctx context.Context, sessionID uuid.UUID, message locitypes.ConversationMessage) error
}

type Deps struct {
	LLM      Generator
	Store    Store
	Trips    Trips
	Plans    Plans
	Hotels   Hotels
	Places   geocode.Forward
	Regen    Regenerator
	Sessions Sessions
	Logger   *slog.Logger
	Now      func() time.Time
}

type Service struct{ d Deps }

func NewService(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d}
}

// Propose turns message into stored proposals for the caller's trip. No
// proposals and a nil error means the message asked for no change.
func (s *Service) Propose(ctx context.Context, userID, tripID, sessionID uuid.UUID, message string) ([]Proposal, error) {
	t, err := s.d.Trips.GetTrip(ctx, tripID, userID)
	if err != nil {
		return nil, err
	}
	now := s.d.Now()
	actions, err := Extract(ctx, s.d.LLM, message, snapshotOf(t, now))
	if err != nil || len(actions) == 0 {
		return nil, err
	}
	sessionID = s.ownedSession(ctx, userID, sessionID)

	out := make([]Proposal, 0, len(actions))
	for _, a := range actions {
		p := Proposal{UserID: userID, TripID: tripID, SessionID: sessionID, Action: a, ExpiresAt: now.Add(proposalTTL)}
		s.resolve(ctx, userID, &p)
		if err := s.d.Store.Create(ctx, &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func snapshotOf(t *trip.Trip, now time.Time) Snapshot {
	snap := Snapshot{Days: len(t.Days), Today: now}
	for _, c := range t.Cities {
		snap.Cities = append(snap.Cities, c.CityName)
	}
	if len(snap.Cities) == 0 && t.CityName != "" {
		snap.Cities = []string{t.CityName}
	}
	if t.StartDate != nil && t.EndDate != nil {
		snap.StartDate, snap.EndDate = t.StartDate.Format(time.DateOnly), t.EndDate.Format(time.DateOnly)
	}
	return snap
}

// ownedSession keeps sessionID only if it is the caller's thread.
func (s *Service) ownedSession(ctx context.Context, userID, sessionID uuid.UUID) uuid.UUID {
	if sessionID == uuid.Nil || s.d.Sessions == nil {
		return uuid.Nil
	}
	sess, err := s.d.Sessions.GetSession(ctx, sessionID)
	if err != nil || sess == nil || sess.UserID != userID {
		return uuid.Nil
	}
	return sessionID
}

// resolve writes the card's summary and, for pick-one actions, its options.
// A lookup that fails leaves a card that says so rather than failing the turn.
func (s *Service) resolve(ctx context.Context, userID uuid.UUID, p *Proposal) {
	a := p.Action
	switch a.Kind {
	case KindSetDates:
		p.Summary = "Set the trip's dates to " + dayRange(a.StartDate, a.EndDate) + "."
	case KindRegenerateDays:
		p.Summary = fmt.Sprintf("Re-plan the itinerary as %d days.", a.Days)
	case KindSearchHotels:
		opts, err := s.hotelOptions(ctx, userID, a)
		if err != nil {
			s.d.Logger.WarnContext(ctx, "trip action: hotel search failed", slog.Any("error", err))
		}
		p.Options = opts
		if len(opts) == 0 {
			p.Summary = fmt.Sprintf("No %s hotels found near %s.", starsLabel(a), a.City)
			return
		}
		p.Summary = fmt.Sprintf("%s hotels in %s: pick one to stay at.", starsLabel(a), a.City)
	case KindSearchFlights:
		f, err := s.flightOption(a)
		if err != nil {
			s.d.Logger.WarnContext(ctx, "trip action: flight links failed", slog.Any("error", err))
			p.Summary = fmt.Sprintf("Couldn't build a flight search for %s → %s.", a.Origin.Name, a.Destination.Name)
			return
		}
		p.Options = []Option{f}
		p.Summary = fmt.Sprintf("Flights %s → %s, %s, %d traveller(s): save this search to the trip.",
			a.Origin.Name, a.Destination.Name, flightDates(a), a.Passengers)
	}
}

func starsLabel(a Action) string {
	switch {
	case a.MinStars == 0 && a.MaxStars == 5:
		return "Hotels"
	case a.MinStars == a.MaxStars:
		return fmt.Sprintf("%d★", a.MinStars)
	default:
		return fmt.Sprintf("%d–%d★", a.MinStars, a.MaxStars)
	}
}

func (s *Service) hotelOptions(ctx context.Context, userID uuid.UUID, a Action) ([]Option, error) {
	places, err := s.d.Places.Search(ctx, a.City, 1)
	if err != nil || len(places) == 0 {
		return nil, err
	}
	// Stars are filtered here, not by the search: its own filter compares the
	// stored text for equality, so "4" never matches "4 stars" or "4.5".
	found, err := s.d.Hotels.GetNearbyHotels(ctx, userID, places[0].Lat, places[0].Lon, hotelRadiusMeters, "", "")
	if err != nil {
		return nil, err
	}
	var keep []locitypes.POIDetailedInfo
	for _, h := range found {
		if starsWithin(h.StarRating, a.MinStars, a.MaxStars) {
			keep = append(keep, h)
		}
	}
	sort.SliceStable(keep, func(i, j int) bool { return keep[i].Rating > keep[j].Rating })
	if len(keep) > maxHotelOptions {
		keep = keep[:maxHotelOptions]
	}
	opts := make([]Option, 0, len(keep))
	for _, h := range keep {
		stars := ""
		if v, ok := starsOf(h.StarRating); ok {
			stars = strconv.FormatFloat(v, 'f', -1, 64)
		}
		stay := &trip.TripStay{CityName: a.City, Name: h.Name, StarRating: stars}
		if h.ID != uuid.Nil {
			stay.POIID = h.ID.String()
		}
		// Only an https site becomes the booking link friends can tap.
		if strings.HasPrefix(h.Website, "https://") {
			site := h.Website
			stay.BookingURL = &site
		}
		label := h.Name
		if stars != "" {
			label += " · " + stars + "★"
		}
		opts = append(opts, Option{Label: label, Detail: h.Address, Stay: stay})
	}
	return opts, nil
}

func (s *Service) flightOption(a Action) (Option, error) {
	depart, _ := time.Parse(time.DateOnly, a.Depart) // normalize checked it
	var ret *time.Time
	if a.Return != "" {
		r, _ := time.Parse(time.DateOnly, a.Return)
		ret = &r
	}
	q := flights.Query{
		Origin:      flights.Place{Name: a.Origin.Name, IATA: a.Origin.IATA},
		Destination: flights.Place{Name: a.Destination.Name, IATA: a.Destination.IATA},
		Depart:      depart, Return: ret, Passengers: a.Passengers, Cabin: cabins[a.Cabin],
	}
	links, err := s.d.Plans.FlightLinks(q)
	if err != nil {
		return Option{}, err
	}
	return Option{
		Label:  "Save this flight search",
		Detail: fmt.Sprintf("%s → %s", a.Origin.Name, a.Destination.Name),
		Flight: &trip.TripFlight{
			Origin: q.Origin, Destination: q.Destination, DepartDate: depart, ReturnDate: ret,
			Passengers: int32(a.Passengers), Cabin: q.Cabin, Links: links,
		},
	}, nil
}

// Apply makes the change p proposes. The proposal is claimed before the
// change and given back if the change fails, so a double tap applies once
// and a failure can be retried.
func (s *Service) Apply(ctx context.Context, userID, proposalID uuid.UUID, option *int, baseVersion int64) (*trip.Trip, *locitypes.ConversationMessage, error) {
	p, err := s.d.Store.Get(ctx, proposalID, userID)
	if err != nil {
		return nil, nil, err
	}
	if p.Status != StatusPending {
		return nil, nil, ErrNotPending
	}
	if !s.d.Now().Before(p.ExpiresAt) {
		return nil, nil, ErrExpired
	}
	opt, err := pick(p, option)
	if err != nil {
		return nil, nil, err
	}
	if err := s.d.Store.Transition(ctx, p.ID, StatusPending, StatusApplied); err != nil {
		return nil, nil, err
	}
	t, err := s.apply(ctx, userID, p, opt, baseVersion)
	if err != nil {
		if rerr := s.d.Store.Transition(ctx, p.ID, StatusApplied, StatusPending); rerr != nil {
			s.d.Logger.WarnContext(ctx, "trip action: could not give a failed proposal back", slog.Any("error", rerr))
		}
		return nil, nil, err
	}
	return t, s.confirm(ctx, p, opt), nil
}

// pick is the option a pick-one action is applied with. Flights have one,
// taken by default; hotels need the traveller's choice.
func pick(p *Proposal, option *int) (*Option, error) {
	switch p.Action.Kind {
	case KindSearchHotels:
		if option == nil || *option < 0 || *option >= len(p.Options) {
			return nil, ErrNoOption
		}
		return &p.Options[*option], nil
	case KindSearchFlights:
		i := 0
		if option != nil {
			i = *option
		}
		if i < 0 || i >= len(p.Options) {
			return nil, ErrNoOption
		}
		return &p.Options[i], nil
	default:
		return nil, nil
	}
}

func (s *Service) apply(ctx context.Context, userID uuid.UUID, p *Proposal, opt *Option, base int64) (*trip.Trip, error) {
	a := p.Action
	switch a.Kind {
	case KindSetDates:
		start, _ := time.Parse(time.DateOnly, a.StartDate)
		end, _ := time.Parse(time.DateOnly, a.EndDate)
		return s.d.Plans.SetDates(ctx, userID, p.TripID, base, start, end)
	case KindSearchHotels:
		return s.d.Plans.SetStay(ctx, userID, p.TripID, base, *opt.Stay)
	case KindSearchFlights:
		return s.d.Plans.AddFlight(ctx, userID, p.TripID, base, *opt.Flight)
	case KindRegenerateDays:
		// Check before generating: a re-plan takes a minute of model time,
		// and a stale or multi-city trip would only be refused at the end.
		t, err := s.d.Trips.GetTrip(ctx, p.TripID, userID)
		if err != nil {
			return nil, err
		}
		if t.Version != base {
			return nil, trip.ErrVersionConflict
		}
		if len(t.Cities) > 1 {
			return nil, fmt.Errorf("%w: re-planning a multi-city trip from chat is not supported yet", trip.ErrInvalidEdit)
		}
		days, err := s.d.Regen.GenerateDays(ctx, userID, t.CityName, a.Days)
		if err != nil {
			return nil, fmt.Errorf("re-plan %s: %w", t.CityName, err)
		}
		return s.d.Plans.ReplaceDays(ctx, userID, p.TripID, base, days)
	}
	return nil, fmt.Errorf("%w: unknown action %q", trip.ErrInvalidEdit, a.Kind)
}

// confirm builds the thread message for an applied proposal and posts it
// when the proposal came from a thread. Posting is best effort: the trip has
// already changed.
func (s *Service) confirm(ctx context.Context, p *Proposal, opt *Option) *locitypes.ConversationMessage {
	var text string
	switch p.Action.Kind {
	case KindSetDates:
		text = "Dates set: " + dayRange(p.Action.StartDate, p.Action.EndDate) + "."
	case KindSearchHotels:
		text = fmt.Sprintf("Staying at %s in %s.", opt.Stay.Name, opt.Stay.CityName)
	case KindSearchFlights:
		text = fmt.Sprintf("Flight search saved: %s → %s, %s.", p.Action.Origin.Name, p.Action.Destination.Name, flightDates(p.Action))
	case KindRegenerateDays:
		text = fmt.Sprintf("Itinerary re-planned as %d days.", p.Action.Days)
	}
	msg := locitypes.ConversationMessage{
		ID: uuid.New(), Role: locitypes.RoleAssistant, Content: text, MessageType: locitypes.TypeResponse,
		Timestamp: s.d.Now(), Origin: locitypes.OriginProactive, SourceLabel: sourceLabel,
	}
	if p.SessionID != uuid.Nil && s.d.Sessions != nil {
		if err := s.d.Sessions.AddMessageToSession(ctx, p.SessionID, msg); err != nil {
			s.d.Logger.WarnContext(ctx, "trip action applied but confirmation not posted", slog.Any("error", err))
		}
	}
	return &msg
}

// Dismiss drops the caller's proposal.
func (s *Service) Dismiss(ctx context.Context, userID, proposalID uuid.UUID) error {
	p, err := s.d.Store.Get(ctx, proposalID, userID)
	if err != nil {
		return err
	}
	return s.d.Store.Transition(ctx, p.ID, StatusPending, StatusDismissed)
}

func dayRange(start, end string) string {
	s, err1 := time.Parse(time.DateOnly, start)
	e, err2 := time.Parse(time.DateOnly, end)
	if err1 != nil || err2 != nil {
		return start + " – " + end
	}
	return s.Format("2 Jan") + " – " + e.Format("2 Jan 2006")
}

func flightDates(a Action) string {
	if a.Return == "" {
		if d, err := time.Parse(time.DateOnly, a.Depart); err == nil {
			return d.Format("2 Jan 2006") + " (one way)"
		}
		return a.Depart + " (one way)"
	}
	return dayRange(a.Depart, a.Return)
}
```

- [ ] **Step 4: Run the tests, then commit**

Run: `go test ./internal/domain/tripaction/`
Expected: PASS.

```bash
git add internal/domain/tripaction/service.go internal/domain/tripaction/service_test.go
git commit -m "feat(tripaction): propose, apply once, dismiss"
```

---

### Task 9: Proposal to proto

**Files:**
- Create `internal/domain/tripaction/proto.go`.
- Test in `internal/domain/tripaction/proto_test.go`.

**Interfaces:**
- Consumes: `trip.StayToProto` and `trip.FlightToProto` (Task 4), and `chatv1` (Task 2).
- Produces: `func ToProto(p *Proposal) *chatv1.ActionProposal`

- [ ] **Step 1: Failing test**

```go
package tripaction

import (
	"testing"
	"time"

	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
)

func TestToProto(t *testing.T) {
	p := &Proposal{
		ID: uuid.New(), TripID: uuid.New(), Summary: "4★ hotels in Lisbon",
		Action:    Action{Kind: KindSearchHotels, City: "Lisbon", MinStars: 4, MaxStars: 4},
		Options:   []Option{{Label: "Hotel Avenida · 4★", Stay: &trip.TripStay{CityName: "Lisbon", Name: "Hotel Avenida"}}},
		ExpiresAt: time.Now(),
	}
	got := ToProto(p)
	require.Equal(t, p.ID.String(), got.GetId())
	require.EqualValues(t, 4, got.GetAction().GetSearchHotels().GetMinStars())
	require.Equal(t, "Hotel Avenida", got.GetOptions()[0].GetStay().GetName())

	f := ToProto(&Proposal{Action: Action{Kind: KindSearchFlights, Origin: Place{Name: "NYC"}, Destination: Place{Name: "Lisbon", IATA: "LIS"}, Depart: "2026-11-12", Passengers: 2, Cabin: "business"}})
	sf := f.GetAction().GetSearchFlights()
	require.Nil(t, sf.GetOrigin().Iata)
	require.Equal(t, "LIS", sf.GetDestination().GetIata())
	require.Nil(t, sf.ReturnDate)
	require.Equal(t, tripv1.FlightCabin_FLIGHT_CABIN_BUSINESS, sf.GetCabin())
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/domain/tripaction/ -run TestToProto`
Expected: FAIL with `undefined: ToProto`.

- [ ] **Step 3: Implement**

```go
package tripaction

import (
	"github.com/FACorreiaa/go-utils/pkg/util"
	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
)

// ToProto is the card a client renders for p.
func ToProto(p *Proposal) *chatv1.ActionProposal {
	out := &chatv1.ActionProposal{
		Id: p.ID.String(), TripId: p.TripID.String(), Summary: p.Summary,
		Action: actionToProto(p.Action), ExpiresAt: timestamppb.New(p.ExpiresAt),
	}
	for _, o := range p.Options {
		po := &chatv1.ActionOption{Label: o.Label, Detail: o.Detail}
		switch {
		case o.Stay != nil:
			po.Choice = &chatv1.ActionOption_Stay{Stay: trip.StayToProto(*o.Stay)}
		case o.Flight != nil:
			po.Choice = &chatv1.ActionOption_Flight{Flight: trip.FlightToProto(*o.Flight)}
		}
		out.Options = append(out.Options, po)
	}
	return out
}

func actionToProto(a Action) *chatv1.TripAction {
	switch a.Kind {
	case KindSetDates:
		return &chatv1.TripAction{Kind: &chatv1.TripAction_SetDates{SetDates: &chatv1.SetDatesAction{
			StartDate: a.StartDate, EndDate: a.EndDate,
		}}}
	case KindSearchHotels:
		return &chatv1.TripAction{Kind: &chatv1.TripAction_SearchHotels{SearchHotels: &chatv1.SearchHotelsAction{
			CityName: a.City, MinStars: int32(a.MinStars), MaxStars: int32(a.MaxStars),
		}}}
	case KindRegenerateDays:
		return &chatv1.TripAction{Kind: &chatv1.TripAction_RegenerateDays{RegenerateDays: &chatv1.RegenerateDaysAction{
			Days: int32(a.Days),
		}}}
	case KindSearchFlights:
		return &chatv1.TripAction{Kind: &chatv1.TripAction_SearchFlights{SearchFlights: &chatv1.SearchFlightsAction{
			Origin:      &tripv1.FlightPlace{Name: a.Origin.Name, Iata: util.StrZeroPtr(a.Origin.IATA)},
			Destination: &tripv1.FlightPlace{Name: a.Destination.Name, Iata: util.StrZeroPtr(a.Destination.IATA)},
			DepartDate:  a.Depart, ReturnDate: util.StrZeroPtr(a.Return),
			Passengers: int32(a.Passengers), Cabin: tripv1.FlightCabin(cabins[a.Cabin]),
		}}}
	}
	return &chatv1.TripAction{}
}
```

- [ ] **Step 4: Run the tests, then commit**

Run: `go test ./internal/domain/tripaction/`
Expected: PASS.

```bash
git add internal/domain/tripaction/proto.go internal/domain/tripaction/proto_test.go
git commit -m "feat(tripaction): proposals on the wire"
```

---

### Task 10: Chat: trip-bound turns propose, and re-plans generate days

**Files:**
- Create `internal/domain/chat/service/chat_trip_actions.go` and `chat_trip_actions_test.go`.
- Modify `internal/domain/chat/service/chat_service.go` to add the `tripActions TripActionProposer` field to `ServiceImpl`, next to `tripRepo`.
- Modify `internal/domain/chat/service/chat_stream_session.go` (the hook at the top of `ProcessUnifiedChatMessageStream`).
- Modify `internal/types/chat_session.go` to add `EventTypeActionProposal = "action_proposal"` to the event-type constants, after `EventTypeGastronomy`.

**Interfaces:**
- Consumes: `tripaction.Proposal` and `tripaction.Generator` (Tasks 6–8).
- Produces:
  - `type TripActionProposer interface{ Propose(ctx, userID, tripID, sessionID uuid.UUID, message string) ([]tripaction.Proposal, error) }`
  - `func (l *ServiceImpl) SetTripActions(p TripActionProposer)`
  - `func (l *ServiceImpl) ActionLLM() tripaction.Generator`
  - `func (l *ServiceImpl) GenerateDays(ctx context.Context, userID uuid.UUID, cityName string, days int) ([]trip.TripDay, error)`, which implements `tripaction.Regenerator`
  - Events with `Type == locitypes.EventTypeActionProposal` and `Data` of type `tripaction.Proposal`

- [ ] **Step 1: Failing tests**

```go
package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/tripaction"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

type fakeProposer struct {
	out []tripaction.Proposal
	err error
	got string
}

func (f *fakeProposer) Propose(_ context.Context, _, _, _ uuid.UUID, msg string) ([]tripaction.Proposal, error) {
	f.got = msg
	return f.out, f.err
}

func drain(ch chan locitypes.StreamEvent) []locitypes.StreamEvent {
	close(ch)
	var evs []locitypes.StreamEvent
	for e := range ch {
		evs = append(evs, e)
	}
	return evs
}

func boundTurn(ch chan locitypes.StreamEvent, msg string) common.ChatContext {
	return common.ChatContext{Ctx: context.Background(), UserID: uuid.New(), TripID: uuid.New(), Message: msg, EventCh: ch, CityName: "Lisbon"}
}

func TestBoundTurn_ProposalsReplaceGeneration(t *testing.T) {
	l := newStreamService(t, &TestLLMClient{})
	l.runCityFn = func(common.ChatContext) (*locitypes.AiCityResponse, error) {
		t.Fatal("a turn that proposes changes must not regenerate")
		return nil, nil
	}
	l.SetTripActions(&fakeProposer{out: []tripaction.Proposal{{ID: uuid.New(), Summary: "Set dates"}, {ID: uuid.New(), Summary: "Hotels"}}})
	ch := make(chan locitypes.StreamEvent, 10)
	require.NoError(t, l.ProcessUnifiedChatMessageStream(boundTurn(ch, "12 to 17 Nov, 4-star hotels")))
	evs := drain(ch)
	require.Len(t, evs, 3)
	require.Equal(t, locitypes.EventTypeActionProposal, evs[0].Type)
	require.Equal(t, "Set dates", evs[0].Message)
	require.IsType(t, tripaction.Proposal{}, evs[0].Data)
	require.Equal(t, locitypes.EventTypeComplete, evs[2].Type)
	require.True(t, evs[2].IsFinal)
}

func TestBoundTurn_NoChangeAnswersButNeverSavesANewTrip(t *testing.T) {
	for name, p := range map[string]*fakeProposer{
		"nothing asked":     {},
		"extraction failed": {err: errors.New("model down")},
	} {
		t.Run(name, func(t *testing.T) {
			l := newStreamService(t, &TestLLMClient{})
			l.SetTripActions(p)
			var ran common.ChatContext
			l.runCityFn = func(cc common.ChatContext) (*locitypes.AiCityResponse, error) { ran = cc; return &locitypes.AiCityResponse{}, nil }
			ch := make(chan locitypes.StreamEvent, 10)
			cc := boundTurn(ch, "what's good for dinner?")
			cc.StopRun = true // skip multi-city planning in this unit test
			require.NoError(t, l.ProcessUnifiedChatMessageStream(cc))
			drain(ch)
			require.True(t, ran.SuppressTripSave, "a trip-bound turn never creates a trip")
			require.Equal(t, cc.TripID, ran.TripID)
		})
	}
}

func TestUnboundTurn_NeverAsksTheProposer(t *testing.T) {
	l := newStreamService(t, &TestLLMClient{})
	p := &fakeProposer{}
	l.SetTripActions(p)
	l.runCityFn = func(cc common.ChatContext) (*locitypes.AiCityResponse, error) {
		require.False(t, cc.SuppressTripSave)
		return &locitypes.AiCityResponse{}, nil
	}
	ch := make(chan locitypes.StreamEvent, 10)
	cc := boundTurn(ch, "3 days in Lisbon")
	cc.TripID, cc.StopRun = uuid.Nil, true
	require.NoError(t, l.ProcessUnifiedChatMessageStream(cc))
	drain(ch)
	require.Empty(t, p.got)
}

func TestGenerateDays_RunsAPresetCityWithoutSaving(t *testing.T) {
	l := newStreamService(t, &TestLLMClient{})
	l.runCityFn = func(cc common.ChatContext) (*locitypes.AiCityResponse, error) {
		require.True(t, cc.StopRun)
		require.True(t, cc.SuppressTripSave)
		require.Equal(t, 2, cc.PresetTripDays)
		require.Equal(t, "Lisbon", cc.CityName)
		cc.EventCh <- locitypes.StreamEvent{Type: locitypes.EventTypeProgress} // nobody reads a re-plan's stream; must not block
		return &locitypes.AiCityResponse{PointsOfInterest: []locitypes.POIDetailedInfo{
			{Name: "Belém", Day: 1}, {Name: "Alfama", Day: 1}, {Name: "Sintra", Day: 2},
		}}, nil
	}
	days, err := l.GenerateDays(context.Background(), uuid.New(), "Lisbon", 2)
	require.NoError(t, err)
	require.Len(t, days, 2)
	require.Len(t, days[0].Stops, 2)
}

func TestGenerateDays_NothingGenerated(t *testing.T) {
	l := newStreamService(t, &TestLLMClient{})
	l.runCityFn = func(common.ChatContext) (*locitypes.AiCityResponse, error) { return &locitypes.AiCityResponse{}, nil }
	_, err := l.GenerateDays(context.Background(), uuid.New(), "Lisbon", 2)
	require.Error(t, err)
}
```

Before running, check two names:
- **`EventTypeProgress`:** `grep -n 'EventTypeProgress' internal/types/chat_session.go`. If the constant has another name, use it.
- **`newStreamService`:** it is the existing builder in `chat_multicity_test.go`. Check that it leaves `tripActions` nil.

- [ ] **Step 2: Run them to see them fail**

Run: `go test -p 2 ./internal/domain/chat/service/ -run 'TestBoundTurn|TestUnboundTurn|TestGenerateDays'`
Expected: FAIL to compile with `l.SetTripActions undefined`.

- [ ] **Step 3: Implement `chat_trip_actions.go`**

```go
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"google.golang.org/genai"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/tripaction"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// TripActionProposer turns a message about a trip into proposed changes.
type TripActionProposer interface {
	Propose(ctx context.Context, userID, tripID, sessionID uuid.UUID, message string) ([]tripaction.Proposal, error)
}

// SetTripActions lets turns bound to a trip propose changes to it instead of
// regenerating. Nil (the default) answers every turn as before.
func (l *ServiceImpl) SetTripActions(p TripActionProposer) { l.tripActions = p }

// ActionLLM is the extraction call trip actions make: this service's model
// chain, behind the same LLM slots every generation takes.
func (l *ServiceImpl) ActionLLM() tripaction.Generator { return actionLLM{l} }

type actionLLM struct{ l *ServiceImpl }

func (a actionLLM) GenerateText(ctx context.Context, prompt string) (string, error) {
	release, err := a.l.acquireLLMSlot(ctx)
	if err != nil {
		return "", fmt.Errorf("LLM capacity exceeded: %w", err)
	}
	defer release()
	return a.l.aiClient.GenerateText(ctx, prompt, &genai.GenerateContentConfig{Temperature: genai.Ptr[float32](0.1)})
}

// proposeTripActions handles a trip-bound turn that asks for changes: one
// action_proposal event per change, then completion. false means the
// message asked for none, or extraction failed, and the turn is answered as
// usual; extraction trouble never costs the traveller their answer.
func (l *ServiceImpl) proposeTripActions(cc common.ChatContext) bool {
	props, err := l.tripActions.Propose(cc.Ctx, cc.UserID, cc.TripID, cc.RequestedSessionID, cc.Message)
	if err != nil {
		l.logger.WarnContext(cc.Ctx, "trip actions: proposing failed; answering instead", slog.Any("error", err))
		return false
	}
	if len(props) == 0 {
		return false
	}
	for _, p := range props {
		l.sendEvent(cc.Ctx, cc.EventCh, locitypes.StreamEvent{
			Type: locitypes.EventTypeActionProposal, Message: p.Summary, Data: p,
		}, 3)
	}
	l.sendEvent(cc.Ctx, cc.EventCh, locitypes.StreamEvent{
		Type: locitypes.EventTypeComplete, Data: "Turn completed.", IsFinal: true,
	}, 3)
	return true
}

// GenerateDays plans days for city without saving a trip, for a confirmed
// "re-plan as N days". It runs the same per-city generation a multi-city stop
// does (runStop): city preset, trip length preset, no trip save.
func (l *ServiceImpl) GenerateDays(ctx context.Context, userID uuid.UUID, cityName string, days int) ([]trip.TripDay, error) {
	ch := make(chan locitypes.StreamEvent, 100)
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range ch { // nobody watches a re-plan's stream
		}
	}()
	cc := common.ChatContext{
		Ctx: ctx, UserID: userID, CityName: cityName,
		Message: fmt.Sprintf("Plan a %d-day itinerary in %s", days, cityName),
		EventCh: ch, StopRun: true, PresetTripDays: days, SuppressTripSave: true,
	}
	data, err := l.runCity(cc)
	close(ch)
	<-drained
	if err != nil {
		return nil, err
	}
	cc.TripDays = days
	tr := buildTripFromCityResponse(&cc, data, "")
	if tr == nil || len(tr.Days) == 0 {
		return nil, errors.New("the re-plan produced no days")
	}
	return tr.Days, nil
}
```

- [ ] **Step 4: The hook and the field**

In `chat_service.go` `ServiceImpl`, after `tripRepo trip.Repository`:

```go
	// Optional, attached with SetTripActions: turns bound to a trip propose
	// changes to it (dates, hotels, re-plan, flights) instead of regenerating.
	tripActions TripActionProposer
```

In `chat_stream_session.go`, at the start of `ProcessUnifiedChatMessageStream`, before `if !cc.StopRun {`:

```go
	// A turn bound to a trip (ChatRequest.trip_id) is about that trip. When it
	// asks for changes they are proposed, not regenerated; otherwise it is
	// answered as usual, but never saved as a new trip, since the traveller
	// is editing one.
	if cc.TripID != uuid.Nil && l.tripActions != nil {
		if l.proposeTripActions(cc) {
			return nil
		}
		cc.SuppressTripSave = true
	}
```

In `internal/types/chat_session.go`, add to the event constants after `EventTypeGastronomy = "gastronomy"`:

```go
	// EventTypeActionProposal carries a tripaction.Proposal: a change to the
	// turn's trip the traveller can confirm with ApplyTripAction.
	EventTypeActionProposal = "action_proposal"
```

- [ ] **Step 5: Run the tests, then commit**

Run: `go test -p 2 ./internal/domain/chat/... ./internal/types/...`
Expected: PASS.

```bash
git add internal/domain/chat/service/chat_trip_actions.go internal/domain/chat/service/chat_trip_actions_test.go \
  internal/domain/chat/service/chat_service.go internal/domain/chat/service/chat_stream_session.go internal/types/chat_session.go
git commit -m "feat(chat): trip-bound turns propose changes; re-plans generate days without saving"
```

---

### Task 11: Chat handler: the event, and Apply/Dismiss RPCs

**Files:**
- Create `internal/domain/chat/handler/trip_actions.go` and `trip_actions_test.go`.
- Modify `internal/domain/chat/handler/chat_handler.go`: add the field to `ChatHandler`, add a case in `mapEventToProto` after the `EventTypeGastronomy` case, and add a case in `eventTypeToProto`.

**Interfaces:**
- Produces:
  - `type TripActions interface{ Apply(...) (*trip.Trip, *locitypes.ConversationMessage, error); Dismiss(...) error }`
  - `func (h *ChatHandler) WithTripActions(a TripActions) *ChatHandler`
  - `ApplyTripAction` and `DismissTripAction`

- [ ] **Step 1: Failing tests**

```go
package handler

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/tripaction"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

func TestMapEventToProto_ActionProposal(t *testing.T) {
	p := tripaction.Proposal{ID: uuid.New(), TripID: uuid.New(), Summary: "Set dates",
		Action: tripaction.Action{Kind: tripaction.KindSetDates, StartDate: "2026-11-12", EndDate: "2026-11-17"}, ExpiresAt: time.Now()}
	resp, err := (&ChatHandler{}).mapEventToProto(context.Background(),
		locitypes.StreamEvent{Type: locitypes.EventTypeActionProposal, EventID: "e1", Data: p}, uuid.New())
	require.NoError(t, err)
	require.Equal(t, chatv1.StreamEventType_STREAM_EVENT_TYPE_ACTION_PROPOSAL, resp.GetEventType())
	got := resp.GetActionProposal().GetProposal()
	require.Equal(t, p.ID.String(), got.GetId())
	require.Equal(t, "2026-11-12", got.GetAction().GetSetDates().GetStartDate())
}

type fakeTripActions struct{ err error }

func (f fakeTripActions) Apply(context.Context, uuid.UUID, uuid.UUID, *int, int64) (*trip.Trip, *locitypes.ConversationMessage, error) {
	if f.err != nil {
		return nil, nil, f.err
	}
	return &trip.Trip{ID: uuid.New(), Title: "Lisbon", Version: 4}, &locitypes.ConversationMessage{Content: "Dates set"}, nil
}

func (f fakeTripActions) Dismiss(context.Context, uuid.UUID, uuid.UUID) error { return f.err }

func signedIn() context.Context {
	return context.WithValue(context.Background(), interceptors.UserIDKey, uuid.NewString())
}

func TestApplyTripAction(t *testing.T) {
	h := (&ChatHandler{}).WithTripActions(fakeTripActions{})
	res, err := h.ApplyTripAction(signedIn(), connect.NewRequest(&chatv1.ApplyTripActionRequest{ProposalId: uuid.NewString(), BaseVersion: 3}))
	require.NoError(t, err)
	require.EqualValues(t, 4, res.Msg.GetTrip().GetVersion())
	require.Equal(t, "Dates set", res.Msg.GetConfirmation().GetContent())
}

func TestTripActionErrorCodes(t *testing.T) {
	for err, code := range map[error]connect.Code{
		tripaction.ErrNotFound:   connect.CodeNotFound,
		trip.ErrNotFound:         connect.CodeNotFound,
		tripaction.ErrNotPending: connect.CodeFailedPrecondition,
		tripaction.ErrExpired:    connect.CodeFailedPrecondition,
		trip.ErrVersionConflict:  connect.CodeFailedPrecondition,
		tripaction.ErrNoOption:   connect.CodeInvalidArgument,
		trip.ErrInvalidEdit:      connect.CodeInvalidArgument,
	} {
		h := (&ChatHandler{}).WithTripActions(fakeTripActions{err: err})
		_, got := h.ApplyTripAction(signedIn(), connect.NewRequest(&chatv1.ApplyTripActionRequest{ProposalId: uuid.NewString()}))
		require.Equal(t, code, connect.CodeOf(got), err.Error())
	}
	_, err := (&ChatHandler{}).ApplyTripAction(signedIn(), connect.NewRequest(&chatv1.ApplyTripActionRequest{ProposalId: uuid.NewString()}))
	require.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
	_, err = (&ChatHandler{}).WithTripActions(fakeTripActions{}).DismissTripAction(context.Background(),
		connect.NewRequest(&chatv1.DismissTripActionRequest{ProposalId: uuid.NewString()}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}
```

`mapEventToProto`'s signature on main is `(ctx, event, userID)`. Check that `userID` is a `uuid.UUID`, with `grep -n 'func (h \*ChatHandler) mapEventToProto' chat_handler.go`, and match it.

- [ ] **Step 2: Run them to see them fail**

Run: `go test -p 2 ./internal/domain/chat/handler/ -run 'ActionProposal|TripAction'`
Expected: FAIL to compile with `undefined: WithTripActions` (and `GetActionProposal` resolves once Task 2 is in).

- [ ] **Step 3: Implement `trip_actions.go`**

```go
package handler

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/presenter"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/tripaction"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// TripActions applies and dismisses what the agent proposed (tripaction.Service).
type TripActions interface {
	Apply(ctx context.Context, userID, proposalID uuid.UUID, option *int, baseVersion int64) (*trip.Trip, *locitypes.ConversationMessage, error)
	Dismiss(ctx context.Context, userID, proposalID uuid.UUID) error
}

// WithTripActions attaches ApplyTripAction / DismissTripAction. Without it
// they answer Unimplemented.
func (h *ChatHandler) WithTripActions(a TripActions) *ChatHandler {
	h.tripActions = a
	return h
}

var errTripActionsOff = errors.New("trip actions are not enabled")

func (h *ChatHandler) tripActionCaller(ctx context.Context, proposalID string) (uuid.UUID, uuid.UUID, error) {
	if h.tripActions == nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeUnimplemented, errTripActionsOff)
	}
	s, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || s == "" {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}
	id, err := uuid.Parse(proposalID)
	if err != nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid proposal ID"))
	}
	return userID, id, nil
}

func (h *ChatHandler) ApplyTripAction(ctx context.Context, req *connect.Request[chatv1.ApplyTripActionRequest]) (*connect.Response[chatv1.ApplyTripActionResponse], error) {
	userID, id, err := h.tripActionCaller(ctx, req.Msg.GetProposalId())
	if err != nil {
		return nil, err
	}
	var option *int
	if req.Msg.OptionIndex != nil {
		v := int(req.Msg.GetOptionIndex())
		option = &v
	}
	t, msg, err := h.tripActions.Apply(ctx, userID, id, option, req.Msg.GetBaseVersion())
	if err != nil {
		return nil, h.tripActionError(ctx, err)
	}
	res := &chatv1.ApplyTripActionResponse{Trip: trip.ToProto(t)}
	if msg != nil {
		res.Confirmation = presenter.ToConversationMessage(*msg)
	}
	return connect.NewResponse(res), nil
}

func (h *ChatHandler) DismissTripAction(ctx context.Context, req *connect.Request[chatv1.DismissTripActionRequest]) (*connect.Response[chatv1.DismissTripActionResponse], error) {
	userID, id, err := h.tripActionCaller(ctx, req.Msg.GetProposalId())
	if err != nil {
		return nil, err
	}
	if err := h.tripActions.Dismiss(ctx, userID, id); err != nil {
		return nil, h.tripActionError(ctx, err)
	}
	return connect.NewResponse(&chatv1.DismissTripActionResponse{}), nil
}

func (h *ChatHandler) tripActionError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, tripaction.ErrNotFound), errors.Is(err, trip.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, tripaction.ErrNotPending), errors.Is(err, tripaction.ErrExpired), errors.Is(err, trip.ErrVersionConflict):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, tripaction.ErrNoOption), errors.Is(err, trip.ErrInvalidEdit):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		if h.logger != nil {
			h.logger.ErrorContext(ctx, "trip action failed", "error", err)
		}
		return connect.NewError(connect.CodeInternal, errors.New("the change could not be applied"))
	}
}
```

- [ ] **Step 4: Handler field and event mapping**

Add to the `ChatHandler` struct in `chat_handler.go`:

```go
	tripActions TripActions
```

Add to `mapEventToProto`, after the `EventTypeGastronomy` case:

```go
	case locitypes.EventTypeActionProposal:
		var p tripaction.Proposal
		if !decodeData(event.Data, &p) {
			return nil, fmt.Errorf("action proposal event %q: undecodable data", event.EventID)
		}
		resp.Payload = &chatv1.StreamEvent_ActionProposal{ActionProposal: &chatv1.ActionProposalPayload{
			Proposal: tripaction.ToProto(&p),
		}}
```

Add to `eventTypeToProto`, before `default:`:

```go
	case locitypes.EventTypeActionProposal:
		return chatv1.StreamEventType_STREAM_EVENT_TYPE_ACTION_PROPOSAL
```

Then add the `tripaction` import.

- [ ] **Step 5: Run the tests, then commit**

Run: `go test -p 2 ./internal/domain/chat/...`
Expected: PASS.

```bash
git add internal/domain/chat/handler/trip_actions.go internal/domain/chat/handler/trip_actions_test.go internal/domain/chat/handler/chat_handler.go
git commit -m "feat(chat): stream action proposals; ApplyTripAction and DismissTripAction"
```

---

### Task 12: Wiring, iOS compatibility, PR

**Files:**
- Modify `cmd/api/dependencies.go`, inside `if d.DB != nil { … }` right after the `d.TripHandler = d.TripHandler.WithPlan(d.TripService)` line.
- Modify loci-ios: the `StreamEvent` payload switch in `Features/Search/Model/SearchState.swift` (around line 166), plus any other exhaustive `switch` on the payload.

- [ ] **Step 1: Wire it**

```go
		// The chat agent's write path: trip-bound turns propose changes, and
		// ApplyTripAction makes the one the traveller confirms.
		if chatImpl, ok := d.ChatService.(*chatservice.ServiceImpl); ok {
			tripActions := tripaction.NewService(tripaction.Deps{
				LLM:      chatImpl.ActionLLM(),
				Store:    tripaction.NewStore(d.DB.Pool),
				Trips:    d.TripRepo,
				Plans:    d.TripService,
				Hotels:   d.POISvc,
				Places:   newForwardGeocoder(d.AppCache),
				Regen:    chatImpl,
				Sessions: d.ChatRepo,
				Logger:   d.Logger,
			})
			chatImpl.SetTripActions(tripActions)
			d.ChatHandler = d.ChatHandler.WithTripActions(tripActions)
		}
```

Add the `tripaction` import. If `d.POISvc` or `d.ChatRepo` has another field name, use what `dependencies.go` assigns at its construction (`grep -n 'POISvc\|ChatRepo' cmd/api/dependencies.go`).

- [ ] **Step 2: Whole suite, integration, lint**

Run (with the Docker env from Global Constraints):

```bash
go build ./... && go test -p 4 ./... && go test -tags=integration -p 1 -count=1 ./internal/domain/tripaction/ ./internal/domain/trip/ && make lint-go
```

Expected: everything passes, and lint reports `0 issues.`

- [ ] **Step 3: Server PR**

```bash
git add cmd/api/dependencies.go && git commit -m "feat(tripaction): wire the agent's trip actions"
git push -u origin feat/trip-agent-actions && gh pr create --fill
```

- [ ] **Step 4: iOS compatibility, merged with the proto bump**

iOS builds against the local proto package, and an exhaustive Swift `switch` on the payload oneof stops compiling at the new case. In `loci-ios`:

```bash
cd ~/Work/production/apps/Loci/loci-ios && git fetch origin
git worktree add -b chore/action-proposal-case /private/tmp/ios-action-case origin/main
grep -rn 'case \.gastronomy' --include=*.swift /private/tmp/ios-action-case/loci
```

In each `switch` that lists `.gastronomy`, add:

```swift
        case .actionProposal:
            // Plan 4 renders proposal cards; until then the stream ignores them.
            break
```

Then build for the simulator, as the project's `scripts/` do (for example `xcodebuild -scheme loci -destination 'generic/platform=iOS Simulator' build`). Open the PR. Merge it alongside the proto bump.

---

### Task 13: Deploy and prove

- [ ] **Step 1:** Merge the server PR. If CD opens no promote PR, open one by hand in `~/Work/production/platform/infra`: bump `image.tag` in `apps/loci/api/values-production.yaml` and the API image in the rerank and bundle-forge jobs. Migration 0110 needs no Postgres change.
- [ ] **Step 2:** After the sync, an unauthenticated POST with `{}` to `/loci.chat.ChatService/ApplyTripAction` and `/DismissTripAction` returns 400 or 401, not 404.
- [ ] **Step 3:** Signed in, from a trip, StreamChat with `trip_id` and "4-star hotels, 12 to 17 November, make it 4 days":
  - Expect three `action_proposal` events and a complete event.
  - `ApplyTripAction` on the dates card bumps the trip version.
  - `GetTrip` shows the dates.
  - Log the extraction success rate over 10 messages on the free model chain.

---

## Spec deviations (deliberate)

- **No `set_constraint` or `add_poi`/`remove_poi` actions.** They are not in the owner's ask. The keyword path still handles POI edits.
- **ContinueChat (and so Telegram) is not wired here.** The `trip_id` on `ContinueChatRequest` and the proposals on `ChatResponse` go with plan 5, which owns Telegram. Web and iOS follow-ups use `StreamChat`.
- **No `ActionAppliedPayload` stream event.** Apply is unary and returns the trip.
- **Expiry is computed at read time** rather than stored as an `expired` status.
- **Regeneration runs at apply time, inside the unary `ApplyTripAction`.** It takes about a minute on the free chain. If the client timeout is shorter, plans 3 and 4 show progress and re-fetch the trip.
- **A bound turn with proposals does not log the user's message into the session.** Only the confirmation is posted.
