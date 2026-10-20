# User-Submitted Places Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a signed-in user propose a place that is not yet in the database, and have it become a real POI once a second distinct user confirms it exists.

**Architecture:** Submissions live in their own table and are never written to `points_of_interest` until corroborated, so the retrieval layer that feeds the LLM cannot be poisoned by construction. Promotion goes through the existing `UpsertPOIByIdentity`, inheriting the identity rules already enforced on POIs. Corroboration reuses the two-distinct-users mechanic proven by the place-claim flow.

**Tech Stack:** Go 1.27, ConnectRPC, Postgres (pgx v5, goose migrations), protobuf via buf; SolidJS/SolidStart client with TanStack solid-query.

**Spec:** `docs/superpowers/specs/2026-09-18-user-submitted-places-design.md`

## Global Constraints

- **Identity is `(city_id, lower(btrim(name)))`** in both `points_of_interest` and `place_submissions`. Copy the expression verbatim; never introduce a second normalisation.
- **Nothing reaches `points_of_interest` before promotion.** No task may write a pending submission into that table.
- **`placesRequiredForPromotion = 2`** distinct users, the submitter counting as the first.
- **Migration number is `0094`.** Main is at 0093. Run `bash scripts/check-migrations.sh` before committing any migration; a duplicate version blocked goose on main once already.
- **Every RPC on this service requires auth.** Use the existing `userID(ctx)` helper, which returns `CodeUnauthenticated`.
- **Integration tests use the `integration` build tag** and are skipped unless `PLACEINTEL_TEST_DSN` is set. Local dev DB: container `postgres-loci-connect-api-db`, host port **5459**, credentials in `loci-connect-server/.env`.
- **Proto changes are a release, not an edit.** Merge, tag, `go get` on the server; `buf push` plus a `package.json` bump on the client. The newest tag is often somebody else's — confirm it contains your change before building on it.
- **Shared checkouts.** Other sessions commit in these repos. Stage files explicitly by path; never `git add -A`.

---

## File Structure

**Proto** (`loci-connect-proto`)
- Modify `proto/loci/place/place_intelligence.proto` — three RPCs and their messages.

**Server** (`loci-connect-server`)
- Create `pkg/db/migrations/0094_place_submissions.up.sql` — the two tables.
- Create `internal/domain/placeintel/submissions.go` — all submission SQL, kept out of `handler.go`, which is already ~600 lines.
- Modify `internal/domain/placeintel/handler.go` — the three RPC methods, delegating to `submissions.go`.
- Create `internal/domain/placeintel/submissions_integration_test.go` — the corroboration and promotion rules.
- Modify `cmd/api/dependencies.go` and `cmd/api/router.go` only if the handler gains new dependencies (it gains the POI repository and city resolver).

**Client** (`loci-client`)
- Modify `src/lib/api/place-intelligence.ts` — submission hooks.
- Create `src/components/contribute/AddPlaceForm.tsx` — the submission form.
- Create `src/components/contribute/PendingPlaceCard.tsx` — a confirm-this-place task.
- Modify `src/routes/contribute.tsx` — surface both.

---

### Task 1: Proto contract

**Files:**
- Modify: `loci-connect-proto/proto/loci/place/place_intelligence.proto`

**Interfaces:**
- Consumes: nothing.
- Produces: `SubmitPlace`, `ConfirmPlace`, `ListPendingPlaces` RPCs; messages `SubmitPlaceRequest/Response`, `ConfirmPlaceRequest/Response`, `ListPendingPlacesRequest/Response`, `PendingPlace`; enum `PlaceSubmissionStatus`.

- [ ] **Step 1: Add the messages and RPCs**

Append to `proto/loci/place/place_intelligence.proto`, above the `service` block:

```proto
enum PlaceSubmissionStatus {
  PLACE_SUBMISSION_STATUS_UNSPECIFIED = 0;
  PLACE_SUBMISSION_STATUS_PENDING = 1;
  PLACE_SUBMISSION_STATUS_ACCEPTED = 2;
  PLACE_SUBMISSION_STATUS_REJECTED = 3;
}

message SubmitPlaceRequest {
  string client_submission_id = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 100
    uuid: true
  }];
  string name = 2 [(buf.validate.field).string = {
    min_len: 2
    max_len: 300
  }];
  // Resolved server-side; the client cannot be relied on to know a city id.
  string city_name = 3 [(buf.validate.field).string = {
    min_len: 1
    max_len: 200
  }];
  optional string country = 4;
  optional string category = 5;
  optional double latitude = 6 [(buf.validate.field).double = {
    gte: -90
    lte: 90
  }];
  optional double longitude = 7 [(buf.validate.field).double = {
    gte: -180
    lte: 180
  }];
  optional string address = 8;
  optional string website = 9;
}

message SubmitPlaceResponse {
  string submission_id = 1;
  PlaceSubmissionStatus status = 2;
  // How many more distinct people must confirm before this becomes a place.
  int32 confirmations_needed = 3;
}

message ConfirmPlaceRequest {
  string submission_id = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 100
    uuid: true
  }];
}

message ConfirmPlaceResponse {
  PlaceSubmissionStatus status = 1;
  int32 confirmations_needed = 2;
  // Set once promoted, so the client can link straight to the new place.
  optional string poi_id = 3;
}

message PendingPlace {
  string submission_id = 1;
  string name = 2;
  string city_name = 3;
  optional string category = 4;
  optional string address = 5;
  int32 confirmations_needed = 6;
}

message ListPendingPlacesRequest {
  int32 limit = 1 [(buf.validate.field).int32 = {
    gte: 1
    lte: 50
  }];
}

message ListPendingPlacesResponse {
  repeated PendingPlace places = 1;
}
```

Then add to the `service PlaceIntelligenceService` block:

```proto
  rpc SubmitPlace(SubmitPlaceRequest) returns (SubmitPlaceResponse);
  rpc ConfirmPlace(ConfirmPlaceRequest) returns (ConfirmPlaceResponse);
  rpc ListPendingPlaces(ListPendingPlacesRequest) returns (ListPendingPlacesResponse);
```

- [ ] **Step 2: Verify the schema compiles and lints**

Run: `cd loci-connect-proto && buf build && buf lint`
Expected: both exit 0 with no output.

- [ ] **Step 3: Regenerate committed output**

Run: `cd loci-connect-proto && buf generate`

Then check what moved: `git status --short`. Expect changes only under `gen/` for `loci/place`. If the auth, user, interest or localcontext output also changes, that is toolchain churn from a local plugin version — `git checkout --` those files and leave them out of this change.

- [ ] **Step 4: Commit and release**

```bash
cd loci-connect-proto
git add proto/loci/place/place_intelligence.proto gen
git commit -m "place: contracts for submitting and confirming a place"
```

Open a PR, merge it, then tag. Confirm the tag contains this change before using it:

```bash
git fetch origin --tags
git tag --contains $(git rev-parse origin/main) | tail -3
```

---

### Task 2: Migration 0094

**Files:**
- Create: `loci-connect-server/pkg/db/migrations/0094_place_submissions.up.sql`

**Interfaces:**
- Consumes: nothing.
- Produces: tables `place_submissions`, `place_submission_confirmations`.

- [ ] **Step 1: Write the migration**

```sql
-- +goose Up
-- +goose StatementBegin

-- A place somebody says exists, held outside points_of_interest until a second
-- person agrees.
--
-- Keeping it out of that table is the whole safety property: the retrieval layer
-- reads points_of_interest, so anything written there can reach a real
-- itinerary. Nothing here is visible to the generator, which means an unverified
-- submission cannot be recommended no matter what any later query forgets to
-- filter.
CREATE TABLE IF NOT EXISTS place_submissions (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_submission_id UUID NOT NULL UNIQUE,
    user_id              UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    city_id              UUID NOT NULL REFERENCES cities (id) ON DELETE CASCADE,
    name                 TEXT NOT NULL,
    category             TEXT,
    latitude             DOUBLE PRECISION,
    longitude            DOUBLE PRECISION,
    address              TEXT,
    website              TEXT,
    status               TEXT NOT NULL DEFAULT 'pending'
                         CHECK (status IN ('pending', 'accepted', 'rejected')),
    poi_id               UUID REFERENCES points_of_interest (id) ON DELETE SET NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Identity, copied verbatim from migration 0068's index on
-- points_of_interest, which is what UpsertPOIByIdentity conflicts on. Sharing
-- the expression is what makes "the same place" mean the same thing in both
-- tables. Partial, so a rejected submission does not block a later honest one.
CREATE UNIQUE INDEX IF NOT EXISTS idx_place_submissions_identity
    ON place_submissions (city_id, lower(btrim(name)))
    WHERE status <> 'rejected';

CREATE INDEX IF NOT EXISTS idx_place_submissions_city_status
    ON place_submissions (city_id, status);

-- One row per person per submission, so COUNT(*) is already a count of
-- distinct people and nobody can confirm the same place twice.
CREATE TABLE IF NOT EXISTS place_submission_confirmations (
    submission_id UUID NOT NULL REFERENCES place_submissions (id) ON DELETE CASCADE,
    user_id       UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (submission_id, user_id)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS place_submission_confirmations;
DROP TABLE IF EXISTS place_submissions;
-- +goose StatementEnd
```

- [ ] **Step 2: Check the version does not collide**

Run: `cd loci-connect-server && bash scripts/check-migrations.sh`
Expected: `check-migrations: N migrations, no duplicate versions.`

- [ ] **Step 3: Apply it to the local dev database**

```bash
cd loci-connect-server
U=$(grep -E '^POSTGRES_USER=' .env | cut -d= -f2)
D=$(grep -E '^POSTGRES_DB=' .env | cut -d= -f2)
docker exec -i postgres-loci-connect-api-db psql -U "$U" -d "$D" -v ON_ERROR_STOP=1 \
  < pkg/db/migrations/0094_place_submissions.up.sql
```

Expected: `CREATE TABLE`, `CREATE INDEX` … with no error.

- [ ] **Step 4: Commit**

```bash
git add pkg/db/migrations/0094_place_submissions.up.sql
git commit -m "Hold submitted places outside points_of_interest until confirmed"
```

---

### Task 3: Submission store

**Files:**
- Create: `loci-connect-server/internal/domain/placeintel/submissions.go`
- Test: `loci-connect-server/internal/domain/placeintel/submissions_integration_test.go`

**Interfaces:**
- Consumes: migration 0094.
- Produces:
  - `const placesRequiredForPromotion = 2`
  - `type submission struct { ID, UserID, CityID uuid.UUID; Name, CityName string; Category, Address, Website *string; Lat, Lng *float64; Status string; POIID *uuid.UUID; Confirmations int32 }`
  - `func confirmationsNeeded(confirmations int32) int32`
  - `func (h *Handler) existingPOIByIdentity(ctx context.Context, cityID uuid.UUID, name string) (uuid.UUID, bool, error)`
  - `func (h *Handler) insertSubmission(ctx context.Context, tx pgx.Tx, s submission, clientID string) (uuid.UUID, error)`

- [ ] **Step 1: Write the failing test**

Create `internal/domain/placeintel/submissions_integration_test.go`:

```go
//go:build integration

package placeintel

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The submitter is the first voice, so a fresh submission needs exactly one
// more person.
func TestConfirmationsNeeded(t *testing.T) {
	t.Parallel()
	assert.Equal(t, int32(1), confirmationsNeeded(0), "submitter counts as one")
	assert.Equal(t, int32(0), confirmationsNeeded(1), "one confirmation is enough")
	assert.Equal(t, int32(0), confirmationsNeeded(5), "never negative")
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd loci-connect-server && go test -tags=integration ./internal/domain/placeintel/ -run TestConfirmationsNeeded`
Expected: FAIL — `undefined: confirmationsNeeded`.

- [ ] **Step 3: Write the store**

Create `internal/domain/placeintel/submissions.go`:

```go
package placeintel

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// placesRequiredForPromotion is how many distinct people must say a place
// exists before it becomes one. The submitter is the first, so in practice one
// other person is needed — the same shape as the claim flow, where filing a
// report is itself the first observation.
const placesRequiredForPromotion = 2

// submission is a place somebody says exists, before anybody has agreed.
type submission struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	CityID        uuid.UUID
	Name          string
	CityName      string
	Category      *string
	Address       *string
	Website       *string
	Lat           *float64
	Lng           *float64
	Status        string
	POIID         *uuid.UUID
	Confirmations int32
}

// confirmationsNeeded is how many more people must speak up, never negative.
func confirmationsNeeded(confirmations int32) int32 {
	needed := placesRequiredForPromotion - 1 - confirmations
	if needed < 0 {
		return 0
	}
	return needed
}

// existingPOIByIdentity reports whether this place is already on the guide.
//
// The expression matches migration 0068's unique index, which is what
// UpsertPOIByIdentity conflicts on, so "already exists" means exactly what it
// means everywhere else.
func (h *Handler) existingPOIByIdentity(ctx context.Context, cityID uuid.UUID, name string) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := h.db.QueryRow(ctx, `
		SELECT id FROM points_of_interest
		WHERE city_id = $1 AND lower(btrim(name)) = lower(btrim($2))`, cityID, name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, connect.NewError(connect.CodeInternal, fmt.Errorf("look up existing place: %w", err))
	}
	return id, true, nil
}

// insertSubmission records the proposal, or returns the existing row when this
// client id has been seen before so a retry cannot create a second one.
func (h *Handler) insertSubmission(ctx context.Context, tx pgx.Tx, s submission, clientID string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO place_submissions
			(client_submission_id, user_id, city_id, name, category, latitude, longitude, address, website)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (client_submission_id) DO NOTHING
		RETURNING id`,
		clientID, s.UserID, s.CityID, s.Name, s.Category, s.Lat, s.Lng, s.Address, s.Website).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx, `
			SELECT id FROM place_submissions WHERE client_submission_id = $1`, clientID).Scan(&id); err != nil {
			return uuid.Nil, connect.NewError(connect.CodeInternal, fmt.Errorf("read duplicate submission: %w", err))
		}
		return id, nil
	}
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInternal, fmt.Errorf("insert submission: %w", err))
	}
	return id, nil
}
```

- [ ] **Step 4: Run the test and watch it pass**

Run: `go test -tags=integration ./internal/domain/placeintel/ -run TestConfirmationsNeeded`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/placeintel/submissions.go internal/domain/placeintel/submissions_integration_test.go
git commit -m "Store for submitted places, sharing the POI identity expression"
```

---

### Task 4: SubmitPlace

**Files:**
- Modify: `loci-connect-server/internal/domain/placeintel/handler.go`
- Modify: `loci-connect-server/internal/domain/placeintel/submissions_integration_test.go`

**Interfaces:**
- Consumes: `submission`, `insertSubmission`, `existingPOIByIdentity`, `confirmationsNeeded` (Task 3); `cityResolver` (added here).
- Produces: `func (h *Handler) SubmitPlace(ctx context.Context, req *connect.Request[placev1.SubmitPlaceRequest]) (*connect.Response[placev1.SubmitPlaceResponse], error)`; `Handler.cities` field of interface type `cityResolver`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/domain/placeintel/submissions_integration_test.go`:

```go
func TestSubmitPlaceIsPendingAndInvisible(t *testing.T) {
	pool := testPool(t)
	handler := newSubmissionHandler(t, pool)
	cityID := seedCity(t, pool)
	alice := seedScout(t, pool)

	response, err := handler.SubmitPlace(ctxAs(alice), connect.NewRequest(&placev1.SubmitPlaceRequest{
		ClientSubmissionId: uuid.NewString(),
		Name:               "Tasca do Fernando",
		CityName:           cityNameFor(t, pool, cityID),
	}))
	require.NoError(t, err)
	assert.Equal(t, placev1.PlaceSubmissionStatus_PLACE_SUBMISSION_STATUS_PENDING, response.Msg.GetStatus())
	assert.Equal(t, int32(1), response.Msg.GetConfirmationsNeeded())

	// The safety property: nothing the generator reads has been touched.
	var pois int
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT COUNT(*)::int FROM points_of_interest WHERE lower(btrim(name)) = 'tasca do fernando'`).Scan(&pois))
	assert.Equal(t, 0, pois, "a pending place must not be in points_of_interest")
}

func TestSubmitPlaceIsIdempotent(t *testing.T) {
	pool := testPool(t)
	handler := newSubmissionHandler(t, pool)
	cityID := seedCity(t, pool)
	alice := seedScout(t, pool)
	clientID := uuid.NewString()

	req := func() *connect.Request[placev1.SubmitPlaceRequest] {
		return connect.NewRequest(&placev1.SubmitPlaceRequest{
			ClientSubmissionId: clientID,
			Name:               "Padaria Nova",
			CityName:           cityNameFor(t, pool, cityID),
		})
	}
	first, err := handler.SubmitPlace(ctxAs(alice), req())
	require.NoError(t, err)
	second, err := handler.SubmitPlace(ctxAs(alice), req())
	require.NoError(t, err)
	assert.Equal(t, first.Msg.GetSubmissionId(), second.Msg.GetSubmissionId(), "a retry must not create a second row")
}

func TestSubmitPlaceRejectsOneAlreadyOnTheGuide(t *testing.T) {
	pool := testPool(t)
	handler := newSubmissionHandler(t, pool)
	cityID, poiID := seedPlaceInCity(t, pool)
	_ = poiID
	alice := seedScout(t, pool)

	_, err := handler.SubmitPlace(ctxAs(alice), connect.NewRequest(&placev1.SubmitPlaceRequest{
		ClientSubmissionId: uuid.NewString(),
		Name:               "  test cafe  ", // same place, sloppier typing
		CityName:           cityNameFor(t, pool, cityID),
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeAlreadyExists, connect.CodeOf(err))
}
```

Add these helpers to the same file (alongside the existing `seedScout`, `ctxAs`, `testPool`):

```go
// newSubmissionHandler builds a Handler with the city resolver stubbed to a
// city that already exists, because resolving a city is not what these tests
// are about.
func newSubmissionHandler(t *testing.T, pool *pgxpool.Pool) *Handler {
	t.Helper()
	h := NewHandler(pool, nil)
	h.cities = stubResolver{pool: pool}
	return h
}

type stubResolver struct{ pool *pgxpool.Pool }

func (s stubResolver) ResolveCity(ctx context.Context, name, country string) (uuid.UUID, string, error) {
	var id uuid.UUID
	var found string
	err := s.pool.QueryRow(ctx, `SELECT id, name FROM cities WHERE name = $1`, name).Scan(&id, &found)
	return id, found, err
}

func seedCity(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO cities (id, name, country, center_location)
		VALUES ($1, $2, 'Testland', ST_SetSRID(ST_MakePoint(0, 0), 4326))`,
		id, "Testville-"+id.String()[:8])
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM cities WHERE id = $1`, id)
	})
	return id
}

func cityNameFor(t *testing.T, pool *pgxpool.Pool, cityID uuid.UUID) string {
	t.Helper()
	var name string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT name FROM cities WHERE id = $1`, cityID).Scan(&name))
	return name
}

// seedPlaceInCity creates a city and a POI named "Test Cafe" inside it.
func seedPlaceInCity(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	cityID := seedCity(t, pool)
	poiID := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO points_of_interest (id, name, location, city_id)
		VALUES ($1, 'Test Cafe', ST_SetSRID(ST_MakePoint(0, 0), 4326), $2)`, poiID, cityID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM points_of_interest WHERE id = $1`, poiID)
	})
	return cityID, poiID
}
```

- [ ] **Step 2: Run them and watch them fail**

Run:
```bash
U=$(grep -E '^POSTGRES_USER=' .env | cut -d= -f2)
P=$(grep -E '^POSTGRES_PASSWORD=' .env | cut -d= -f2)
D=$(grep -E '^POSTGRES_DB=' .env | cut -d= -f2)
PLACEINTEL_TEST_DSN="postgres://$U:$P@localhost:5459/$D?sslmode=disable" \
  go test -tags=integration ./internal/domain/placeintel/ -run TestSubmitPlace
```
Expected: FAIL — `h.SubmitPlace undefined` and `h.cities undefined`.

- [ ] **Step 3: Add the resolver seam to the Handler**

In `internal/domain/placeintel/handler.go`, add above `type Handler struct`:

```go
// cityResolver turns what a user typed into a city row, creating it when the
// city is genuinely new. Narrowed to the one method this package needs so the
// tests can stub it without standing up the geocoder.
type cityResolver interface {
	ResolveCity(ctx context.Context, name, country string) (uuid.UUID, string, error)
}
```

Add the field to `Handler`:

```go
	cities cityResolver
```

And a setter used by wiring (Task 7), placed next to `NewHandler`:

```go
// WithCityResolver supplies the resolver used when a submitted place names a
// city. Separate from NewHandler so the existing call sites keep working.
func (h *Handler) WithCityResolver(cities cityResolver) *Handler {
	h.cities = cities
	return h
}
```

- [ ] **Step 4: Implement SubmitPlace**

Append to `internal/domain/placeintel/handler.go`:

```go
func (h *Handler) SubmitPlace(ctx context.Context, req *connect.Request[placev1.SubmitPlaceRequest]) (*connect.Response[placev1.SubmitPlaceResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	if h.cities == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("city resolution is not configured"))
	}

	name := strings.TrimSpace(req.Msg.GetName())
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("a place needs a name"))
	}

	cityID, _, err := h.cities.ResolveCity(ctx, req.Msg.GetCityName(), req.Msg.GetCountry())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("could not place %q in a city: %w", name, err))
	}

	// Already on the guide is a different answer from "new", and the client can
	// act on it — open the place rather than propose a twin.
	if existing, found, err := h.existingPOIByIdentity(ctx, cityID, name); err != nil {
		return nil, err
	} else if found {
		return nil, connect.NewError(connect.CodeAlreadyExists,
			fmt.Errorf("%q is already on the guide (%s)", name, existing))
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("begin submission: %w", err))
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	id, err := h.insertSubmission(ctx, tx, submission{
		UserID:   uid,
		CityID:   cityID,
		Name:     name,
		Category: optionalString(req.Msg.Category),
		Address:  optionalString(req.Msg.Address),
		Website:  optionalString(req.Msg.Website),
		Lat:      req.Msg.Latitude,
		Lng:      req.Msg.Longitude,
	}, req.Msg.GetClientSubmissionId())
	if err != nil {
		return nil, err
	}

	var confirmations int32
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*)::integer FROM place_submission_confirmations WHERE submission_id = $1`, id).
		Scan(&confirmations); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("count confirmations: %w", err))
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("commit submission: %w", err))
	}
	h.logger.InfoContext(ctx, "place submitted", slog.String("submission_id", id.String()))

	return connect.NewResponse(&placev1.SubmitPlaceResponse{
		SubmissionId:         id.String(),
		Status:               placev1.PlaceSubmissionStatus_PLACE_SUBMISSION_STATUS_PENDING,
		ConfirmationsNeeded:  confirmationsNeeded(confirmations),
	}), nil
}

// optionalString normalises an absent-or-blank proto field to a nil column.
func optionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
```

- [ ] **Step 5: Run the tests and watch them pass**

Run the same command as Step 2.
Expected: PASS for all three `TestSubmitPlace*` tests.

- [ ] **Step 6: Commit**

```bash
git add internal/domain/placeintel/handler.go internal/domain/placeintel/submissions_integration_test.go
git commit -m "Accept a submitted place, held pending and off the guide"
```

---

### Task 5: ConfirmPlace and promotion

**Files:**
- Modify: `loci-connect-server/internal/domain/placeintel/submissions.go`
- Modify: `loci-connect-server/internal/domain/placeintel/handler.go`
- Modify: `loci-connect-server/internal/domain/placeintel/submissions_integration_test.go`

**Interfaces:**
- Consumes: everything from Tasks 3 and 4; `poiUpserter` (added here).
- Produces: `func (h *Handler) ConfirmPlace(...)`; `Handler.pois` field of interface type `poiUpserter`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/domain/placeintel/submissions_integration_test.go`:

```go
func TestSecondPersonPromotesTheePlace(t *testing.T) {
	pool := testPool(t)
	handler := newSubmissionHandler(t, pool)
	cityID := seedCity(t, pool)
	alice, bob := seedScout(t, pool), seedScout(t, pool)

	submitted, err := handler.SubmitPlace(ctxAs(alice), connect.NewRequest(&placev1.SubmitPlaceRequest{
		ClientSubmissionId: uuid.NewString(),
		Name:               "Miradouro Novo",
		CityName:           cityNameFor(t, pool, cityID),
	}))
	require.NoError(t, err)

	confirmed, err := handler.ConfirmPlace(ctxAs(bob), connect.NewRequest(&placev1.ConfirmPlaceRequest{
		SubmissionId: submitted.Msg.GetSubmissionId(),
	}))
	require.NoError(t, err)

	assert.Equal(t, placev1.PlaceSubmissionStatus_PLACE_SUBMISSION_STATUS_ACCEPTED, confirmed.Msg.GetStatus())
	assert.Equal(t, int32(0), confirmed.Msg.GetConfirmationsNeeded())
	require.NotEmpty(t, confirmed.Msg.GetPoiId(), "promotion must return the new place")

	// Now — and only now — it exists where the generator can find it.
	var pois int
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT COUNT(*)::int FROM points_of_interest WHERE id = $1`,
		uuid.MustParse(confirmed.Msg.GetPoiId())).Scan(&pois))
	assert.Equal(t, 1, pois)

	// Both people are credited, not just whoever acted last.
	for _, scout := range []uuid.UUID{alice, bob} {
		var accepted int32
		require.NoError(t, pool.QueryRow(context.Background(), `
			SELECT accepted_claims FROM contributor_profiles WHERE user_id = $1`, scout).Scan(&accepted))
		assert.Equal(t, int32(1), accepted, "scout %s was not credited", scout)
	}
}

func TestConfirmingYourOwnSubmissionFails(t *testing.T) {
	pool := testPool(t)
	handler := newSubmissionHandler(t, pool)
	cityID := seedCity(t, pool)
	alice := seedScout(t, pool)

	submitted, err := handler.SubmitPlace(ctxAs(alice), connect.NewRequest(&placev1.SubmitPlaceRequest{
		ClientSubmissionId: uuid.NewString(),
		Name:               "Solo Bar",
		CityName:           cityNameFor(t, pool, cityID),
	}))
	require.NoError(t, err)

	_, err = handler.ConfirmPlace(ctxAs(alice), connect.NewRequest(&placev1.ConfirmPlaceRequest{
		SubmissionId: submitted.Msg.GetSubmissionId(),
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err),
		"corroboration has to come from somebody else")
}
```

Extend `newSubmissionHandler` to stub the upserter as well:

```go
	h.pois = stubUpserter{pool: pool}
```

and add:

```go
type stubUpserter struct{ pool *pgxpool.Pool }

func (s stubUpserter) UpsertPOIByIdentity(ctx context.Context, name string, cityID uuid.UUID, lat, lng float64) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO points_of_interest (id, name, location, city_id)
		VALUES (gen_random_uuid(), $1, ST_SetSRID(ST_MakePoint($2, $3), 4326), $4)
		ON CONFLICT (city_id, lower(btrim(name))) DO UPDATE SET updated_at = NOW()
		RETURNING id`, name, lng, lat, cityID).Scan(&id)
	return id, err
}
```

- [ ] **Step 2: Run them and watch them fail**

Run the Step 2 command from Task 4 with `-run 'TestSecondPerson|TestConfirmingYourOwn'`.
Expected: FAIL — `h.ConfirmPlace undefined`.

- [ ] **Step 3: Add the upserter seam**

In `handler.go`, beside `cityResolver`:

```go
// poiUpserter promotes a confirmed submission into a real place. Narrowed to
// one method, and satisfied in production by poi.RepositoryImpl, so the
// identity rules live in one place rather than being restated here.
type poiUpserter interface {
	UpsertPOIByIdentity(ctx context.Context, name string, cityID uuid.UUID, lat, lng float64) (uuid.UUID, error)
}
```

Add `pois poiUpserter` to `Handler`, and:

```go
// WithPOIUpserter supplies the promotion path used when a submission is
// confirmed.
func (h *Handler) WithPOIUpserter(pois poiUpserter) *Handler {
	h.pois = pois
	return h
}
```

- [ ] **Step 4: Implement ConfirmPlace**

Append to `handler.go`:

```go
func (h *Handler) ConfirmPlace(ctx context.Context, req *connect.Request[placev1.ConfirmPlaceRequest]) (*connect.Response[placev1.ConfirmPlaceResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	submissionID, err := uuid.Parse(req.Msg.GetSubmissionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("unknown submission"))
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("begin confirmation: %w", err))
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var (
		submitter uuid.UUID
		cityID    uuid.UUID
		name      string
		status    string
		poiID     *uuid.UUID
		lat, lng  *float64
	)
	err = tx.QueryRow(ctx, `
		SELECT user_id, city_id, name, status, poi_id, latitude, longitude
		FROM place_submissions WHERE id = $1 FOR UPDATE`, submissionID).
		Scan(&submitter, &cityID, &name, &status, &poiID, &lat, &lng)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("unknown submission"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("load submission: %w", err))
	}

	// Already promoted: say so rather than counting another vote.
	if status == "accepted" && poiID != nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("commit confirmation: %w", err))
		}
		id := poiID.String()
		return connect.NewResponse(&placev1.ConfirmPlaceResponse{
			Status:              placev1.PlaceSubmissionStatus_PLACE_SUBMISSION_STATUS_ACCEPTED,
			ConfirmationsNeeded: 0,
			PoiId:               &id,
		}), nil
	}

	// Corroboration means somebody else. Letting a submitter confirm their own
	// place would make the whole gate decorative.
	if submitter == uid {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("a place needs somebody else to confirm it"))
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO place_submission_confirmations (submission_id, user_id)
		VALUES ($1, $2) ON CONFLICT DO NOTHING`, submissionID, uid); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("record confirmation: %w", err))
	}

	var confirmations int32
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*)::integer FROM place_submission_confirmations WHERE submission_id = $1`, submissionID).
		Scan(&confirmations); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("count confirmations: %w", err))
	}

	if needed := confirmationsNeeded(confirmations); needed > 0 {
		if err := tx.Commit(ctx); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("commit confirmation: %w", err))
		}
		return connect.NewResponse(&placev1.ConfirmPlaceResponse{
			Status:              placev1.PlaceSubmissionStatus_PLACE_SUBMISSION_STATUS_PENDING,
			ConfirmationsNeeded: needed,
		}), nil
	}

	if h.pois == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("promotion is not configured"))
	}
	var latitude, longitude float64
	if lat != nil {
		latitude = *lat
	}
	if lng != nil {
		longitude = *lng
	}
	promoted, err := h.pois.UpsertPOIByIdentity(ctx, name, cityID, latitude, longitude)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("promote submission: %w", err))
	}

	if _, err := tx.Exec(ctx, `
		UPDATE place_submissions
		SET status = 'accepted', poi_id = $2, updated_at = NOW()
		WHERE id = $1`, submissionID, promoted); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("accept submission: %w", err))
	}

	if err := creditPlaceContributors(ctx, tx, submissionID, submitter); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("commit promotion: %w", err))
	}
	h.logger.InfoContext(ctx, "place promoted",
		slog.String("submission_id", submissionID.String()), slog.String("poi_id", promoted.String()))

	id := promoted.String()
	return connect.NewResponse(&placev1.ConfirmPlaceResponse{
		Status:              placev1.PlaceSubmissionStatus_PLACE_SUBMISSION_STATUS_ACCEPTED,
		ConfirmationsNeeded: 0,
		PoiId:               &id,
	}), nil
}
```

- [ ] **Step 5: Credit everybody involved**

Append to `internal/domain/placeintel/submissions.go`:

```go
// creditPlaceContributors rewards the submitter and everybody who confirmed.
//
// Deliberately the same shape as creditCorroborators: reputation used to go
// only to whoever acted last, even though their vote was worth nothing without
// the others it agreed with.
func creditPlaceContributors(ctx context.Context, tx pgx.Tx, submissionID, submitter uuid.UUID) error {
	rows, err := tx.Query(ctx, `
		SELECT user_id FROM place_submission_confirmations WHERE submission_id = $1`, submissionID)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("list confirmers: %w", err))
	}
	defer rows.Close()

	credited := []uuid.UUID{submitter}
	seen := map[uuid.UUID]struct{}{submitter: {}}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return connect.NewError(connect.CodeInternal, fmt.Errorf("scan confirmer: %w", err))
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		credited = append(credited, id)
	}
	if err := rows.Err(); err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("iterate confirmers: %w", err))
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO contributor_profiles (user_id, submitted_claims, accepted_claims, reputation)
		SELECT unnest($1::uuid[]), 0, 1, 3
		ON CONFLICT (user_id) DO UPDATE SET
			accepted_claims = contributor_profiles.accepted_claims + 1,
			reputation = LEAST(100, contributor_profiles.reputation + 3),
			updated_at = NOW()`, credited); err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("credit place contributors: %w", err))
	}
	return nil
}
```

- [ ] **Step 6: Run the tests and watch them pass**

Run the Step 2 command.
Expected: PASS, including the assertion that both scouts show `accepted_claims = 1`.

- [ ] **Step 7: Commit**

```bash
git add internal/domain/placeintel/handler.go internal/domain/placeintel/submissions.go internal/domain/placeintel/submissions_integration_test.go
git commit -m "Promote a place once somebody else confirms it exists"
```

---

### Task 6: ListPendingPlaces

**Files:**
- Modify: `loci-connect-server/internal/domain/placeintel/handler.go`
- Modify: `loci-connect-server/internal/domain/placeintel/submissions_integration_test.go`

**Interfaces:**
- Consumes: Tasks 3–5.
- Produces: `func (h *Handler) ListPendingPlaces(...)`.

- [ ] **Step 1: Write the failing test**

```go
func TestPendingPlacesExcludeYourOwn(t *testing.T) {
	pool := testPool(t)
	handler := newSubmissionHandler(t, pool)
	cityID := seedCity(t, pool)
	alice, bob := seedScout(t, pool), seedScout(t, pool)

	_, err := handler.SubmitPlace(ctxAs(alice), connect.NewRequest(&placev1.SubmitPlaceRequest{
		ClientSubmissionId: uuid.NewString(),
		Name:               "Quiosque do Parque",
		CityName:           cityNameFor(t, pool, cityID),
	}))
	require.NoError(t, err)

	mine, err := handler.ListPendingPlaces(ctxAs(alice),
		connect.NewRequest(&placev1.ListPendingPlacesRequest{Limit: 20}))
	require.NoError(t, err)
	for _, p := range mine.Msg.GetPlaces() {
		assert.NotEqual(t, "Quiosque do Parque", p.GetName(), "you cannot confirm your own place")
	}

	theirs, err := handler.ListPendingPlaces(ctxAs(bob),
		connect.NewRequest(&placev1.ListPendingPlacesRequest{Limit: 20}))
	require.NoError(t, err)
	found := false
	for _, p := range theirs.Msg.GetPlaces() {
		if p.GetName() == "Quiosque do Parque" {
			found = true
			assert.Equal(t, int32(1), p.GetConfirmationsNeeded())
		}
	}
	assert.True(t, found, "somebody else should be asked to confirm it")
}
```

- [ ] **Step 2: Run it and watch it fail**

Run the Task 4 Step 2 command with `-run TestPendingPlacesExcludeYourOwn`.
Expected: FAIL — `h.ListPendingPlaces undefined`.

- [ ] **Step 3: Implement it**

```go
func (h *Handler) ListPendingPlaces(ctx context.Context, req *connect.Request[placev1.ListPendingPlacesRequest]) (*connect.Response[placev1.ListPendingPlacesResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	limit := int(req.Msg.GetLimit())
	if limit <= 0 {
		limit = 10
	}

	rows, err := h.db.Query(ctx, `
		SELECT s.id::text, s.name, c.name, s.category, s.address,
		       (SELECT COUNT(*)::integer FROM place_submission_confirmations f WHERE f.submission_id = s.id)
		FROM place_submissions s
		JOIN cities c ON c.id = s.city_id
		WHERE s.status = 'pending'
		  AND s.user_id <> $1
		  AND NOT EXISTS (
		      SELECT 1 FROM place_submission_confirmations f
		      WHERE f.submission_id = s.id AND f.user_id = $1
		  )
		ORDER BY s.created_at DESC
		LIMIT $2`, uid, limit)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list pending places: %w", err))
	}
	defer rows.Close()

	places := make([]*placev1.PendingPlace, 0, limit)
	for rows.Next() {
		var (
			place         placev1.PendingPlace
			category      *string
			address       *string
			confirmations int32
		)
		if err := rows.Scan(&place.SubmissionId, &place.Name, &place.CityName, &category, &address, &confirmations); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scan pending place: %w", err))
		}
		place.Category = category
		place.Address = address
		place.ConfirmationsNeeded = confirmationsNeeded(confirmations)
		places = append(places, &place)
	}
	if err := rows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("iterate pending places: %w", err))
	}
	return connect.NewResponse(&placev1.ListPendingPlacesResponse{Places: places}), nil
}
```

- [ ] **Step 4: Run the test and watch it pass**

Expected: PASS.

- [ ] **Step 5: Run the whole package and commit**

```bash
PLACEINTEL_TEST_DSN="..." go test -tags=integration ./internal/domain/placeintel/
git add internal/domain/placeintel/handler.go internal/domain/placeintel/submissions_integration_test.go
git commit -m "Offer pending places to everybody except the person who added them"
```

---

### Task 7: Wire the dependencies

**Files:**
- Modify: `loci-connect-server/cmd/api/dependencies.go:787` (where `PlaceIntelligenceHandler` is built)

**Interfaces:**
- Consumes: `WithCityResolver`, `WithPOIUpserter` (Tasks 4–5).
- Produces: a handler that can resolve cities and promote places in production.

- [ ] **Step 1: Find the construction site**

Run: `grep -n "NewHandler" cmd/api/dependencies.go | grep -i place`

- [ ] **Step 2: Supply the collaborators**

Change the construction to chain the two setters, using the city resolver and POI repository already built earlier in that file:

```go
deps.PlaceIntelligenceHandler = placeintel.NewHandler(pool, logger).
	WithCityResolver(cityResolverAdapter{resolver: cityResolver}).
	WithPOIUpserter(poiUpserterAdapter{repo: poiRepository})
```

Add the two adapters at the bottom of `dependencies.go`, which exist because the domain packages' signatures are wider than the narrow interfaces `placeintel` asks for:

```go
// cityResolverAdapter narrows city.ResolverImpl to what placeintel needs.
type cityResolverAdapter struct{ resolver *city.ResolverImpl }

func (a cityResolverAdapter) ResolveCity(ctx context.Context, name, country string) (uuid.UUID, string, error) {
	resolved, err := a.resolver.Resolve(ctx, city.ResolveQuery{Name: name, Country: country})
	if err != nil {
		return uuid.Nil, "", err
	}
	return resolved.ID, resolved.Name, nil
}

// poiUpserterAdapter narrows poi.RepositoryImpl to the promotion call.
type poiUpserterAdapter struct{ repo *poi.RepositoryImpl }

func (a poiUpserterAdapter) UpsertPOIByIdentity(ctx context.Context, name string, cityID uuid.UUID, lat, lng float64) (uuid.UUID, error) {
	id, _, err := a.repo.UpsertPOIByIdentity(ctx, locitypes.POIDetailedInfo{
		Name:      name,
		Latitude:  lat,
		Longitude: lng,
	}, cityID)
	return id, err
}
```

**Check the real field names before writing this.** `city.ResolveQuery` and `city.Resolved` are defined in `internal/domain/city/city_resolver.go`; if the fields are named differently, use the real ones rather than these.

- [ ] **Step 3: Build**

Run: `go build ./... && go vet ./internal/domain/placeintel/ ./cmd/api/`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add cmd/api/dependencies.go
git commit -m "Give the place handler a city resolver and a promotion path"
```

---

### Task 8: Client API surface

**Files:**
- Modify: `loci-client/src/lib/api/place-intelligence.ts`

**Interfaces:**
- Consumes: the proto release from Task 1 (bump `@buf/loci_loci-proto.bufbuild_es` in `package.json` first).
- Produces: `useSubmitPlace()`, `useConfirmPlace()`, `usePendingPlaces(options)`, `type PendingPlace`.

- [ ] **Step 1: Take the new proto**

Run `pnpm buf-update`, or set the `@buf/loci_loci-proto.bufbuild_es` version in `package.json` to the BSR commit containing Task 1, then `pnpm install`.

Verify it arrived: `ls node_modules/@buf/loci_loci-proto.bufbuild_es/loci/place/` should show the regenerated `place_intelligence_pb.js`, and `grep -c SubmitPlace` on it should be non-zero.

- [ ] **Step 2: Add the hooks**

```ts
export interface PendingPlace {
  submissionId: string;
  name: string;
  cityName: string;
  category?: string;
  address?: string;
  confirmationsNeeded: number;
}

/** Places somebody else has proposed, waiting on a second pair of eyes. */
export const usePendingPlaces = (options: PlaceIntelligenceQueryOptions = {}) =>
  useAppQuery(() => ({
    enabled: options.enabled ? options.enabled() : true,
    queryKey: ["place-intelligence", "pending-places"],
    queryFn: async (): Promise<PendingPlace[]> => {
      const response = await placeClient.listPendingPlaces(
        create(ListPendingPlacesRequestSchema, { limit: 20 }),
      );
      return response.places.map((place) => ({
        submissionId: place.submissionId,
        name: place.name,
        cityName: place.cityName,
        category: place.category,
        address: place.address,
        confirmationsNeeded: place.confirmationsNeeded,
      }));
    },
  }));

export const useSubmitPlace = () => {
  const queryClient = useQueryClient();
  return useMutation(() => ({
    mutationFn: async (place: {
      name: string;
      cityName: string;
      country?: string;
      category?: string;
      latitude?: number;
      longitude?: number;
      address?: string;
      website?: string;
    }) => {
      const response = await placeClient.submitPlace(
        create(SubmitPlaceRequestSchema, {
          clientSubmissionId: crypto.randomUUID(),
          ...place,
        }),
      );
      return {
        submissionId: response.submissionId,
        confirmationsNeeded: response.confirmationsNeeded,
      };
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["place-intelligence"] }),
  }));
};

export const useConfirmPlace = () => {
  const queryClient = useQueryClient();
  return useMutation(() => ({
    mutationFn: async (submissionId: string) => {
      const response = await placeClient.confirmPlace(
        create(ConfirmPlaceRequestSchema, { submissionId }),
      );
      return {
        promoted: response.status === ProtoPlaceSubmissionStatus.ACCEPTED,
        confirmationsNeeded: response.confirmationsNeeded,
        poiId: response.poiId,
      };
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["place-intelligence"] }),
  }));
};
```

Add the matching imports from `@buf/loci_loci-proto.bufbuild_es/loci/place/place_intelligence_pb.js`: `ConfirmPlaceRequestSchema`, `ListPendingPlacesRequestSchema`, `SubmitPlaceRequestSchema`, and `PlaceSubmissionStatus as ProtoPlaceSubmissionStatus`.

- [ ] **Step 3: Typecheck and commit**

```bash
pnpm typecheck && npx oxlint src/lib/api/place-intelligence.ts
git add src/lib/api/place-intelligence.ts package.json pnpm-lock.yaml
git commit -m "Client hooks for submitting and confirming a place"
```

---

### Task 9: Add-a-place form

**Files:**
- Create: `loci-client/src/components/contribute/AddPlaceForm.tsx`
- Modify: `loci-client/src/components/contribute/index.ts`

**Interfaces:**
- Consumes: `useSubmitPlace` (Task 8).
- Produces: `<AddPlaceForm />`.

- [ ] **Step 1: Write the component**

```tsx
import { createSignal, Show } from "solid-js";
import { MapPinPlus } from "lucide-solid";
import { useSubmitPlace } from "~/lib/api/place-intelligence";
import { capture } from "~/lib/analytics";
import { ErrorView } from "~/components/ErrorView";
import { Button } from "~/ui/button";

/**
 * Propose a place the guide does not have yet.
 *
 * Nothing submitted here reaches a recommendation until somebody else says the
 * place exists, which is why the form can stay open to any signed-in scout.
 */
export function AddPlaceForm() {
  const submit = useSubmitPlace();
  const [name, setName] = createSignal("");
  const [cityName, setCityName] = createSignal("");
  const [category, setCategory] = createSignal("");

  const ready = () => name().trim().length > 1 && cityName().trim().length > 0;

  const onSubmit = (event: SubmitEvent) => {
    event.preventDefault();
    if (!ready()) return;
    submit.mutate(
      {
        name: name().trim(),
        cityName: cityName().trim(),
        category: category().trim() || undefined,
      },
      {
        onSuccess: () => {
          capture("place_submitted", { city: cityName().trim() });
          setName("");
          setCategory("");
        },
      },
    );
  };

  return (
    <form onSubmit={onSubmit} class="rounded-xl border border-border bg-card p-6">
      <p class="font-coord text-[10px] uppercase tracking-[0.18em] text-accent">Add a place</p>
      <h2 class="mt-2 text-2xl">Something we are missing</h2>
      <p class="mt-2 text-sm text-muted-foreground">
        It goes live once another scout confirms it exists.
      </p>

      <label class="mt-5 block text-sm font-semibold" for="place-name">
        What is it called?
      </label>
      <input
        id="place-name"
        value={name()}
        onInput={(event) => setName(event.currentTarget.value)}
        maxlength={300}
        class="mt-2 w-full rounded-lg border border-border bg-background px-3 py-2 text-sm"
      />

      <label class="mt-4 block text-sm font-semibold" for="place-city">
        Which city?
      </label>
      <input
        id="place-city"
        value={cityName()}
        onInput={(event) => setCityName(event.currentTarget.value)}
        maxlength={200}
        class="mt-2 w-full rounded-lg border border-border bg-background px-3 py-2 text-sm"
      />

      <label class="mt-4 block text-sm font-semibold" for="place-category">
        What kind of place? <span class="font-normal text-muted-foreground">Optional</span>
      </label>
      <input
        id="place-category"
        value={category()}
        onInput={(event) => setCategory(event.currentTarget.value)}
        maxlength={100}
        placeholder="cafe, museum, viewpoint…"
        class="mt-2 w-full rounded-lg border border-border bg-background px-3 py-2 text-sm"
      />

      <Button type="submit" class="mt-5 w-full gap-2" disabled={!ready() || submit.isPending}>
        <MapPinPlus class="h-4 w-4" />
        {submit.isPending ? "Submitting…" : "Add this place"}
      </Button>

      <Show when={submit.isError}>
        <ErrorView error={submit.error} class="mt-4" />
      </Show>

      <Show when={submit.isSuccess}>
        <p class="mt-4 rounded-lg bg-secondary/50 p-3 text-center text-sm">
          Recorded. One more scout needs to confirm this place exists.
        </p>
      </Show>
    </form>
  );
}

export default AddPlaceForm;
```

- [ ] **Step 2: Add the analytics event**

In `src/lib/analytics.ts`, add to the `AnalyticsEvent` union:

```ts
  /** A new place was proposed. Metric: map growth from scouts. */
  | "place_submitted"
```

- [ ] **Step 3: Export it and typecheck**

Add `export { AddPlaceForm } from "./AddPlaceForm";` to `src/components/contribute/index.ts`, then run `pnpm typecheck && npx oxlint .`

- [ ] **Step 4: Commit**

```bash
git add src/components/contribute/AddPlaceForm.tsx src/components/contribute/index.ts src/lib/analytics.ts
git commit -m "A form for proposing a place the guide is missing"
```

---

### Task 10: Confirm-a-place task

**Files:**
- Create: `loci-client/src/components/contribute/PendingPlaceCard.tsx`
- Modify: `loci-client/src/components/contribute/index.ts`
- Modify: `loci-client/src/routes/contribute.tsx`

**Interfaces:**
- Consumes: `usePendingPlaces`, `useConfirmPlace` (Task 8), `AddPlaceForm` (Task 9).
- Produces: the finished `/contribute` surface.

- [ ] **Step 1: Write the card**

```tsx
import { Show } from "solid-js";
import { HelpCircle } from "lucide-solid";
import { type PendingPlace, useConfirmPlace } from "~/lib/api/place-intelligence";
import { Button } from "~/ui/button";

/** "Does this place exist?" — the other half of somebody's submission. */
export function PendingPlaceCard(props: { place: PendingPlace }) {
  const confirm = useConfirmPlace();

  return (
    <div class="rounded-xl border border-border bg-card p-5">
      <div class="flex items-start gap-3">
        <HelpCircle class="mt-0.5 h-5 w-5 text-accent" />
        <div class="min-w-0 flex-1">
          <p class="font-coord text-[9px] uppercase tracking-wider text-muted-foreground">
            Does this place exist?
          </p>
          <h3 class="mt-1 text-lg">{props.place.name}</h3>
          <p class="mt-1 text-xs text-muted-foreground">
            {props.place.cityName}
            <Show when={props.place.category}>{(c) => <> · {c()}</>}</Show>
          </p>
        </div>
      </div>

      <Show
        when={!confirm.isSuccess}
        fallback={
          <p class="mt-4 text-sm text-accent">
            {confirm.data?.promoted
              ? "Confirmed. It is on the guide now."
              : "Thanks — still waiting on one more scout."}
          </p>
        }
      >
        <Button
          class="mt-4 w-full"
          disabled={confirm.isPending}
          onClick={() => confirm.mutate(props.place.submissionId)}
        >
          {confirm.isPending ? "Confirming…" : "Yes, it exists"}
        </Button>
      </Show>
    </div>
  );
}

export default PendingPlaceCard;
```

- [ ] **Step 2: Surface both on /contribute**

In `src/routes/contribute.tsx`, inside the authenticated branch, add the pending places above the existing verification task list and the form in the right column below the claim panel:

```tsx
<Show when={(pendingPlaces.data?.length ?? 0) > 0}>
  <div class="mb-6 grid gap-3">
    <For each={pendingPlaces.data}>{(place) => <PendingPlaceCard place={place} />}</For>
  </div>
</Show>
```

with, alongside the other hooks at the top of the component:

```tsx
const pendingPlaces = usePendingPlaces({ enabled: () => isAuthenticated() });
```

and `<AddPlaceForm />` rendered under the sticky aside.

- [ ] **Step 3: Verify**

```bash
pnpm typecheck && npx oxlint . && npx vitest run
```
Expected: clean, all tests pass.

- [ ] **Step 4: Commit**

```bash
git add src/components/contribute/PendingPlaceCard.tsx src/components/contribute/index.ts src/routes/contribute.tsx
git commit -m "Ask scouts whether a proposed place exists"
```

---

### Task 11: Ship it

**Files:** none — this is deploy and verification.

- [ ] **Step 1: Open PRs**

One per repo: proto (already merged in Task 1), server, client. Wait for CI green on each before merging. The server `proto` job goes red whenever the committed generated output lags the proto module — if that happens, run `buf generate && go mod tidy` and commit the result.

- [ ] **Step 2: Promote by hand**

`INFRA_TOKEN` is unset, so CD's promote job reports success and opens nothing. In `~/Work/production/platform/infra`, set **both** to the merged server commit:
- `apps/loci/api/values-production.yaml` → `tag:`
- `apps/loci/data/postgres.yaml` → the `loci-connect-api-postgres` image

They must move together: a Postgres older than the migrations about to run is a broken deploy. Confirm both images exist in GHCR for that exact SHA before opening the PR.

- [ ] **Step 3: Verify the deploy**

```bash
export KUBECONFIG=~/.kube/maat.yaml
kubectl -n horus rollout status deploy/loci-api --timeout=420s
kubectl -n horus exec loci-postgres-0 -- psql -U loci_production_app -d loci_production \
  -tAc "select max(version_id) from goose_db_version"
```
Expected: rollout succeeds; goose reports 94. Expect the API to restart once or twice while Postgres rolls alongside it — they move together by design.

- [ ] **Step 4: Browser QA — not optional**

Submit a place from one account; confirm from a second; search for it. On this feature every server-side check passed while the live page was rendering an error boundary, so a green fetch is not evidence. Check the service worker (Application → Service Workers → Bypass for network, or an Incognito window) before believing what you see.

---

## Self-Review

**Spec coverage.** Proto → Task 1. Migration → Task 2. Store and identity sharing → Task 3. Submit, dedupe, idempotency, city resolution → Task 4. Confirmation, promotion, crediting, self-confirmation → Task 5. Pending list → Task 6. Wiring → Task 7. Client hooks, form, confirm card → Tasks 8–10. Deploy and browser QA → Task 11. The spec's "out of scope" items (editing, moderation tooling, photos, address geocoding) have no tasks, by design.

**Placeholders.** None: every code step carries the code, and the one genuine unknown — the exact field names on `city.ResolveQuery` / `city.Resolved` — is called out in Task 7 Step 2 with instructions to read the real definition rather than trust the sketch.

**Type consistency.** `confirmationsNeeded(int32) int32`, `existingPOIByIdentity(ctx, uuid.UUID, string) (uuid.UUID, bool, error)`, `UpsertPOIByIdentity(ctx, string, uuid.UUID, float64, float64) (uuid.UUID, error)` and `ResolveCity(ctx, string, string) (uuid.UUID, string, error)` are used with the same shapes in every task that touches them. The narrow interfaces in Tasks 4–5 are what Task 7's adapters satisfy.
