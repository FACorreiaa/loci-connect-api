//go:build integration

package repository

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// GetPOIsBySessionSortedByDistance used to discard its session argument and
// filter on city alone, so it returned every POI ever suggested for a city, by
// anybody. The caller assigns the result straight onto the session's itinerary
// (chat_stream_session.go), so one user's plan could be overwritten with
// another user's places.
//
// The fixture is deliberately adversarial: three sessions in one city, two of
// them belonging to a different user, all with POIs nearer the query point than
// the session under test. If the scoping regresses, the assertion on the names
// fails rather than merely the count.
func TestGetPOIsBySessionSortedByDistance_ScopedToSessionAndUser(t *testing.T) {
	testsupport.Truncate(t, testChatDB,
		"llm_suggested_pois", "llm_interactions", "cities", "users")

	ctx := context.Background()

	mine := newTestUser(t, "session-scope-mine@loci.test")
	theirs := newTestUser(t, "session-scope-theirs@loci.test")

	cityID := uuid.New()
	if _, err := testChatDB.Exec(ctx,
		"INSERT INTO cities (id, name, country) VALUES ($1, $2, $3)",
		cityID, "Funchal", "Portugal"); err != nil {
		t.Fatalf("insert city: %v", err)
	}

	// Places are laid out west to east from the query point so that "sorted by
	// distance" has something real to sort: the decoys sit nearer than mine.
	seed := func(userID uuid.UUID, sessionID uuid.UUID, name string, lon float64) {
		t.Helper()
		var interactionID uuid.UUID
		if err := testChatDB.QueryRow(ctx,
			`INSERT INTO llm_interactions (user_id, session_id, city_id, prompt, response)
			 VALUES ($1, $2, $3, 'p', 'r') RETURNING id`,
			userID, sessionID, cityID).Scan(&interactionID); err != nil {
			t.Fatalf("insert interaction: %v", err)
		}
		if _, err := testChatDB.Exec(ctx,
			`INSERT INTO llm_suggested_pois
			   (user_id, llm_interaction_id, city_id, name, latitude, longitude, location)
			 VALUES ($1, $2, $3, $4, $5, $6, ST_SetSRID(ST_MakePoint($6, $5), 4326))`,
			userID, interactionID, cityID, name, 32.65, lon); err != nil {
			t.Fatalf("insert poi %s: %v", name, err)
		}
	}

	mySession := uuid.New()
	theirSession := uuid.New()
	myOtherSession := uuid.New()

	// Nearest first, so a regression surfaces at the head of the slice.
	seed(theirs, theirSession, "Their Nearest", -16.9080)
	seed(mine, myOtherSession, "My Other Session", -16.9085)
	seed(theirs, theirSession, "Their Second", -16.9090)
	seed(mine, mySession, "Mine Near", -16.9100)
	seed(mine, mySession, "Mine Far", -16.9200)

	repo := NewRepositoryImpl(testChatDB, slog.New(slog.NewTextHandler(io.Discard, nil)))

	pois, err := repo.GetPOIsBySessionSortedByDistance(
		ctx, mine, mySession, cityID,
		locitypes.UserLocation{UserLat: 32.65, UserLon: -16.9070},
	)
	if err != nil {
		t.Fatalf("GetPOIsBySessionSortedByDistance: %v", err)
	}

	got := make([]string, 0, len(pois))
	for _, p := range pois {
		got = append(got, p.Name)
	}
	want := []string{"Mine Near", "Mine Far"}

	if len(got) != len(want) {
		t.Fatalf("got %d POIs %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("POI %d: got %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// A city filter must narrow the session's POIs, never widen them past the
// session — the city argument is optional and used to be the only filter.
func TestGetPOIsBySessionSortedByDistance_NilCityStillScopedToSession(t *testing.T) {
	testsupport.Truncate(t, testChatDB,
		"llm_suggested_pois", "llm_interactions", "cities", "users")

	ctx := context.Background()

	mine := newTestUser(t, "nil-city-mine@loci.test")
	theirs := newTestUser(t, "nil-city-theirs@loci.test")

	cityID := uuid.New()
	if _, err := testChatDB.Exec(ctx,
		"INSERT INTO cities (id, name, country) VALUES ($1, $2, $3)",
		cityID, "Lisbon", "Portugal"); err != nil {
		t.Fatalf("insert city: %v", err)
	}

	insert := func(userID, sessionID uuid.UUID, name string) {
		t.Helper()
		var interactionID uuid.UUID
		if err := testChatDB.QueryRow(ctx,
			`INSERT INTO llm_interactions (user_id, session_id, city_id, prompt, response)
			 VALUES ($1, $2, $3, 'p', 'r') RETURNING id`,
			userID, sessionID, cityID).Scan(&interactionID); err != nil {
			t.Fatalf("insert interaction: %v", err)
		}
		if _, err := testChatDB.Exec(ctx,
			`INSERT INTO llm_suggested_pois
			   (user_id, llm_interaction_id, city_id, name, latitude, longitude, location)
			 VALUES ($1, $2, $3, $4, 38.72, -9.14, ST_SetSRID(ST_MakePoint(-9.14, 38.72), 4326))`,
			userID, interactionID, cityID, name); err != nil {
			t.Fatalf("insert poi: %v", err)
		}
	}

	mySession := uuid.New()
	insert(mine, mySession, "Mine")
	insert(theirs, uuid.New(), "Theirs")

	repo := NewRepositoryImpl(testChatDB, slog.New(slog.NewTextHandler(io.Discard, nil)))

	pois, err := repo.GetPOIsBySessionSortedByDistance(
		ctx, mine, mySession, uuid.Nil,
		locitypes.UserLocation{UserLat: 38.72, UserLon: -9.14},
	)
	if err != nil {
		t.Fatalf("GetPOIsBySessionSortedByDistance: %v", err)
	}
	if len(pois) != 1 || pois[0].Name != "Mine" {
		t.Fatalf("got %+v, want exactly [Mine]", pois)
	}
}
