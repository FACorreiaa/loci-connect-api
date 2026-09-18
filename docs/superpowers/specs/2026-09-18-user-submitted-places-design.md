# User-submitted places

*2026-09-18*

## The problem

Loci's contribution model today is "correct what the AI found". A scout can tell
us a place is quiet, step-free and open until five; they cannot tell us the place
exists. `POIService` has two RPCs — `SearchPOI` and `GetPOI` — and no way to
create anything. Every point of interest in the database was written by the
generation pipeline (`poi/poi_repository.go`, `poi/poi_identity.go`,
`chat/repository/chat_repository.go`), which means the map is exactly as good as
the model's recall and no better.

The place-claim loop proved people will contribute: two scouts independently
confirmed a fact within minutes of it shipping. This extends the same mechanic
from *facts about a place* to *the existence of a place*.

## The constraint that shapes everything

`points_of_interest` is the table the retrieval layer reads
(`retrieval/assemble.go`). Anything written there can end up in a real
itinerary. An unmoderated create endpoint on that table is a poisoning vector:
one joke entry, or one spammer, reaches users as a recommendation.

So the design's central rule is:

> **A submitted place is never written to `points_of_interest` until two
> distinct people say it exists.**

Pending places live in their own table. Retrieval needs no new filter, no
confidence threshold, no "user-submitted" flag to reason about — there is simply
nothing for the generator to find. The safety property holds by construction
rather than by everyone remembering to filter.

## Decisions taken

| Question | Decision |
|---|---|
| When does a new place reach the generator? | Only once corroborated by a second distinct user |
| Who may submit? | Any signed-in user |
| How is location set? | City resolved by the existing resolver; coordinates optional and refining only |

The trust model is what protects the data, which is why the submission gate can
stay open. A reputation threshold was considered and rejected: it would stop a
brand-new user contributing anything, and corroboration already does the work.

## Architecture

### Proto

New RPCs on `PlaceIntelligenceService`
(`loci-connect-proto/proto/loci/place/place_intelligence.proto`) — it is already
the place-knowledge service, and these are place knowledge.

```proto
rpc SubmitPlace(SubmitPlaceRequest) returns (SubmitPlaceResponse);
rpc ConfirmPlace(ConfirmPlaceRequest) returns (ConfirmPlaceResponse);
rpc ListPendingPlaces(ListPendingPlacesRequest) returns (ListPendingPlacesResponse);
```

`SubmitPlaceRequest` carries `client_submission_id` (a UUID, for idempotency —
the same pattern `SubmitPlaceClaim` uses so a retry cannot double-submit),
`name`, `category`, and optional `latitude`/`longitude`, `address`, `website`.

The city is sent as **`city_name` plus optional `country`**, not a `city_id`.
The client cannot be relied on to know a city's id — prod's `cities` table held
three rows as recently as last week — so the server resolves it through the
existing resolver (`city/city_resolver.go`), creating the city row when it is
genuinely new. That resolver was written for exactly this problem when
`/compare` returned 400s for cities nobody had generated yet. A city that cannot
be resolved is an `InvalidArgument`, not a silently orphaned submission.

`SubmitPlaceResponse` returns the submission id, its status, and how many
confirmations it still needs — so the client can say "one more scout" in the
same language the claim flow already uses.

Proto changes are a release, not an edit: merged, tagged, `go get` on the
server, `buf push` plus a `package.json` bump on the client. See the workspace
`CLAUDE.md`.

### Storage — migration 0090

```sql
CREATE TABLE place_submissions (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_submission_id UUID NOT NULL UNIQUE,
    user_id              UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    city_id              UUID NOT NULL REFERENCES cities (id) ON DELETE CASCADE,
    name                 TEXT NOT NULL,
    normalised_name      TEXT NOT NULL,
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

-- Two people submitting the same place race to the same row rather than
-- creating twins. Partial, so a rejected submission does not block a later
-- honest one.
CREATE UNIQUE INDEX idx_place_submissions_identity
    ON place_submissions (city_id, normalised_name)
    WHERE status <> 'rejected';

CREATE TABLE place_submission_confirmations (
    submission_id UUID NOT NULL REFERENCES place_submissions (id) ON DELETE CASCADE,
    user_id       UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (submission_id, user_id)
);
```

The composite primary key on the confirmations table is the corroboration rule
expressed as a constraint: one person cannot confirm the same place twice, so
`COUNT(*)` is already a count of distinct people. The claims flow spends a
`COUNT(DISTINCT user_id)` at read time to get the same guarantee; here it is free.

`normalised_name` is produced by the existing normalisation in
`city/city_normalize.go`, so submissions and POIs agree on what "the same name"
means.

### Promotion

The submitter is scout one — the same reading as the claim flow, where filing a
report is itself the first observation. One further distinct confirmation
promotes the submission:

1. Insert the confirmation. If `COUNT(*) + 1` (the submitter) is below
   `placesRequiredForPromotion = 2`, stop; the place stays invisible.
2. Otherwise call `UpsertPOIByIdentity` to mint or resolve the real POI row.
   This is deliberate reuse: identity is `(city, normalised name)` enforced by
   the unique index from migration 0068, and `poi_identity.go` records that
   coordinates were excluded from identity because the LLM returned the same
   museum kilometres apart. A submission flow inventing its own matching would
   relearn that lesson.
3. Set `status = 'accepted'` and `poi_id`.
4. Credit the submitter and every confirmer, reusing the pattern in
   `creditCorroborators` — the fix that stopped reputation going only to whoever
   acted last.

All four steps run in one transaction, as `SubmitPlaceClaim` does.

### Dedupe on submit

Before inserting, normalise the name and check whether
`(city_id, normalised_name)` already exists in `points_of_interest`. If it does,
return `AlreadyExists` with the existing POI id, so the client can say "this
place is already on the guide" and offer to open it — rather than creating a
duplicate that `UpsertPOIByIdentity` would later silently merge.

### Client

`/contribute` gains two surfaces, reusing what the claim flow already built:

- **Add a place** — name, city (existing resolver/picker), category, and an
  optional coordinate refinement. Submitting shows the same pending language as
  a claim: "Recorded. One more scout needs to confirm this place exists."
- **Confirm a place** — pending submissions in the user's cities appear in the
  verification list as a distinct task type: *"Does this place exist?"* with
  Yes / Not sure. A user never sees their own submission to confirm, because the
  corroboration must come from someone else.

The existing `ClaimResult` component already distinguishes pending from
verified; the place flow reuses it rather than growing a parallel vocabulary.

## Error handling

- Unauthenticated → `Unauthenticated`, as every other method here.
- A city that does not resolve → `InvalidArgument`, naming the city.
- A place that already exists → `AlreadyExists` with the POI id.
- A duplicate `client_submission_id` → return the existing submission unchanged,
  never a second row. This mirrors `SubmitPlaceClaim`'s `ON CONFLICT DO NOTHING`
  followed by a read.
- Confirming your own submission → `FailedPrecondition`. Silently ignoring it
  would let a submitter believe they had promoted their own place.
- Confirming an already-accepted submission → succeed idempotently.

## Testing

The corroboration and promotion rules live in SQL, so they are covered by
integration tests against a real Postgres — the pattern established in
`placeintel/integration_test.go`, behind the `integration` build tag and
`PLACEINTEL_TEST_DSN`. A fake would only prove itself.

- A submitted place is **not** in `points_of_interest` and **not** returned by
  retrieval. This is the safety property; it gets an explicit test.
- A second user's confirmation promotes it, sets `poi_id`, and credits both.
- A third confirmation does not double-credit anyone.
- Submitting a name that already exists in that city returns `AlreadyExists`.
- Resubmitting the same `client_submission_id` returns the same row.
- Confirming your own submission fails.

## Out of scope

Deliberately excluded from the first cut, and worth naming so they are decisions
rather than oversights:

- **Editing or deleting a place** once promoted. Facts about it can already be
  corrected through claims.
- **Rejection and moderation tooling.** `status = 'rejected'` exists in the
  schema so the state is representable, but nothing writes it yet. If spam
  appears, that is the next thing to build — and the pending table makes it
  cheap, because nothing has reached users.
- **Photos.** No object storage exists anywhere in the server.
- **Address geocoding.** The resolver is city-level; a street address would need
  a new external provider.

## Verification

1. Integration tests above, green against a real Postgres.
2. `check-migrations` reports no duplicate versions — a duplicate 0087 blocked
   goose on main once already.
3. In-browser: submit a place from one account, confirm from a second, watch it
   appear in search. Browser QA is not optional here; on this feature every
   server-side check passed while the page was rendering an error boundary.
4. Deploy is a hand-written promote PR moving the API and Postgres tags
   together; `INFRA_TOKEN` is unset, so CD's promote job reports success and
   opens nothing.
