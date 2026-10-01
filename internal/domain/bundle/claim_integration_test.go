//go:build integration

package bundle

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
)

// slowTrips holds every trip write open long enough that concurrent claims
// all get past the unlocked ClaimedTrip lookup before any of them records a
// claim: the window in which two claims used to write two trips.
type slowTrips struct {
	delay time.Duration
}

func (s slowTrips) CreateTripTx(ctx context.Context, tx pgx.Tx, t *trip.Trip) (uuid.UUID, error) {
	time.Sleep(s.delay)
	return trip.TxWriter{}.CreateTripTx(ctx, tx, t)
}

func claimService(delay time.Duration) *Service {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewService(testRepo, slowTrips{delay: delay}, nil, nil, Config{}, logger)
}

func countTrips(t *testing.T, userID uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, testDB.QueryRow(ctx(), `SELECT COUNT(*) FROM trips WHERE user_id = $1`, userID).Scan(&n))
	return n
}

// Concurrent first claims of one pack by one user write exactly one trip, and
// every caller is answered with it. Before ClaimOnce each racer wrote its own
// trip and the losers' copies stayed in the user's trips.
func TestClaim_ConcurrentClaimsWriteOneTrip(t *testing.T) {
	svc := claimService(150 * time.Millisecond)
	bundleID := seedBundle(t, "race-"+uuid.NewString()[:8], false, StatusPublished, 2)
	user := seedUser(t)

	const racers = 2
	var (
		start sync.WaitGroup
		done  sync.WaitGroup
		ids   [racers]uuid.UUID
		errs  [racers]error
	)
	start.Add(1)
	for i := 0; i < racers; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			start.Wait()
			ids[i], errs[i] = svc.Claim(ctx(), user, bundleID)
		}(i)
	}
	start.Done()
	done.Wait()

	for i := 0; i < racers; i++ {
		require.NoError(t, errs[i])
	}
	assert.Equal(t, ids[0], ids[1], "both racers are answered with the same trip")
	assert.Equal(t, 1, countTrips(t, user), "only the winner writes a trip")

	held, found, err := testRepo.ClaimedTrip(ctx(), user, bundleID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, ids[0], held)
}

// The lock is per user and pack: many concurrent claims from several users at
// once give each user one trip, and none waits on another user's claim.
func TestClaim_ConcurrentClaimsAcrossUsers(t *testing.T) {
	svc := claimService(50 * time.Millisecond)
	bundleID := seedBundle(t, "race-many-"+uuid.NewString()[:8], false, StatusPublished, 1)
	users := []uuid.UUID{seedUser(t), seedUser(t), seedUser(t)}

	// More callers than the pool has connections: the first design deadlocked
	// here, every connection held by a waiter and none left for the winner.
	const perUser = 8
	var (
		start sync.WaitGroup
		done  sync.WaitGroup
		mu    sync.Mutex
		got   = map[uuid.UUID]map[uuid.UUID]bool{}
	)
	start.Add(1)
	for _, u := range users {
		got[u] = map[uuid.UUID]bool{}
		for i := 0; i < perUser; i++ {
			done.Add(1)
			go func(u uuid.UUID) {
				defer done.Done()
				start.Wait()
				id, err := svc.Claim(ctx(), u, bundleID)
				assert.NoError(t, err)
				mu.Lock()
				got[u][id] = true
				mu.Unlock()
			}(u)
		}
	}
	start.Done()
	done.Wait()

	seen := map[uuid.UUID]bool{}
	for _, u := range users {
		assert.Len(t, got[u], 1, "every claim by one user returns the same trip")
		assert.Equal(t, 1, countTrips(t, u))
		for id := range got[u] {
			assert.False(t, seen[id], "users never share a trip")
			seen[id] = true
		}
	}
}

// runBackfill applies 0111's Up section, as goose would.
func runBackfill(t *testing.T) {
	t.Helper()
	up, err := os.ReadFile("../../../pkg/db/migrations/0111_bundle_claims_backfill.up.sql")
	require.NoError(t, err)
	body := strings.SplitN(string(up), "-- +goose Down", 2)[0]
	body = strings.NewReplacer("-- +goose Up", "", "-- +goose StatementBegin", "", "-- +goose StatementEnd", "").Replace(body)
	_, err = testDB.Exec(ctx(), body)
	require.NoError(t, err)
}

// Claims made before 0105 left trips but no claim rows. 0111 recognises an
// unedited claimed trip by its fingerprint and records it; anything that no
// longer matches the pack exactly is left alone.
func TestBackfillClaims(t *testing.T) {
	svc := claimService(0)
	slug := "backfill-" + uuid.NewString()[:8]
	bundleID := seedBundle(t, slug, false, StatusPublished, 2)
	b, err := testRepo.GetByID(ctx(), bundleID)
	require.NoError(t, err)
	trips := trip.NewRepository(testDB, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// claimedTrip is exactly what a pre-0105 claim wrote, minus the row.
	write := func(user uuid.UUID) uuid.UUID {
		tr, err := svc.claimedTrip(ctx(), user, b)
		require.NoError(t, err)
		saved, err := trips.SaveTrip(ctx(), tr, 0)
		require.NoError(t, err)
		return saved.ID
	}

	// A double-tapped claim left two copies: the older becomes the claim.
	dup := seedUser(t)
	older := write(dup)
	time.Sleep(10 * time.Millisecond)
	write(dup)

	// A claimed trip the user then edited no longer matches.
	edited := seedUser(t)
	editedTrip := write(edited)
	tr, err := trips.GetTrip(ctx(), editedTrip, edited)
	require.NoError(t, err)
	tr.Days[0].Stops[0].Name = "My own stop"
	_, err = trips.SaveTrip(ctx(), tr, tr.Version)
	require.NoError(t, err)

	// A trip with every pack stop plus one more is not the pack either.
	extra := seedUser(t)
	extraTrip := write(extra)
	tr, err = trips.GetTrip(ctx(), extraTrip, extra)
	require.NoError(t, err)
	tr.Days[1].Stops = append(tr.Days[1].Stops, trip.TripStop{Name: "Bonus", OrderIndex: 9})
	_, err = trips.SaveTrip(ctx(), tr, tr.Version)
	require.NoError(t, err)

	// A user whose claim was recorded after 0105 keeps it.
	recorded := seedUser(t)
	kept, err := svc.Claim(ctx(), recorded, bundleID)
	require.NoError(t, err)
	write(recorded)

	// Same title in another city: someone's own trip, not the pack.
	otherCity := seedUser(t)
	_, err = trips.SaveTrip(ctx(), &trip.Trip{UserID: otherCity, CityName: "Porto", Title: b.Title}, 0)
	require.NoError(t, err)

	runBackfill(t)
	runBackfill(t) // idempotent

	claim := func(user uuid.UUID) (uuid.UUID, bool) {
		id, found, err := testRepo.ClaimedTrip(ctx(), user, bundleID)
		require.NoError(t, err)
		return id, found
	}
	got, found := claim(dup)
	require.True(t, found, "an unedited pre-0105 claim is recorded")
	assert.Equal(t, older, got, "the oldest copy is the claim")
	_, found = claim(edited)
	assert.False(t, found, "an edited trip is not taken for the pack")
	_, found = claim(extra)
	assert.False(t, found, "a superset of the pack is not the pack")
	got, found = claim(recorded)
	require.True(t, found)
	assert.Equal(t, kept, got, "a recorded claim is left alone")
	_, found = claim(otherCity)
	assert.False(t, found)

	// And the recorded claim does what 0105 promised: the next claim returns
	// the old trip instead of writing another.
	again, err := svc.Claim(ctx(), dup, bundleID)
	require.NoError(t, err)
	assert.Equal(t, older, again)
	assert.Equal(t, 2, countTrips(t, dup))
}
