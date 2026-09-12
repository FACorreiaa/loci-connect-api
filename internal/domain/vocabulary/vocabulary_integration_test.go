//go:build integration

package vocabulary

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
)

var testVocabDB *pgxpool.Pool

func TestMain(m *testing.M) {
	testVocabDB = testsupport.MustPool()
	os.Exit(m.Run())
}

func quietPlaces() *Places {
	return New(testVocabDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// seed builds a user with sessions and a city of places, and returns the user.
func seed(t *testing.T, cityName string, places []string, sessionCities ...string) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	userID := uuid.New()
	_, err := testVocabDB.Exec(ctx,
		`INSERT INTO users (id, username, email, password_hash) VALUES ($1, $2, $3, 'x')`,
		userID, "vocab-"+userID.String()[:8], "vocab-"+userID.String()[:8]+"@example.test")
	require.NoError(t, err)

	var cityID uuid.UUID
	err = testVocabDB.QueryRow(ctx,
		`INSERT INTO cities (name, country) VALUES ($1, 'Portugal') RETURNING id`, cityName).Scan(&cityID)
	require.NoError(t, err)

	for _, place := range places {
		_, err := testVocabDB.Exec(ctx,
			`INSERT INTO points_of_interest (name, city_id, location)
			 VALUES ($1, $2, ST_SetSRID(ST_MakePoint(-9.13, 38.72), 4326))`, place, cityID)
		require.NoError(t, err)
	}

	// Inserted oldest first so the ordering assertion means something.
	for i, session := range sessionCities {
		_, err := testVocabDB.Exec(ctx,
			// Every NOT NULL column without a default is supplied; chat_sessions
			// has six.
			`INSERT INTO chat_sessions (id, user_id, city_name, status, created_at, updated_at, expires_at)
			 VALUES ($1, $2, $3, 'active',
			         now() - make_interval(hours => $4),
			         now() - make_interval(hours => $4),
			         now() + interval '30 days')`,
			uuid.New(), userID, session, len(sessionCities)-i)
		require.NoError(t, err)
	}
	return userID
}

// The whole reason this package exists: without these names the transcription
// service answers "Case 2 Soda" for "Cais do Sodré".
func TestTheHintCarriesTheUsersCitiesAndPlaces(t *testing.T) {
	userID := seed(t, "Lisboa",
		[]string{"Cais do Sodré", "Bairro Alto", "Mosteiro dos Jerónimos"},
		"Lisboa")

	hint := quietPlaces().For(context.Background(), userID)

	for _, want := range []string{"Lisboa", "Cais do Sodré", "Bairro Alto", "Mosteiro dos Jerónimos"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint is missing %q: %q", want, hint)
		}
	}
}

// "nearme" is a routing marker, not a place. Offering the decoder a word no
// speaker will ever say spends the hint window for nothing.
func TestTheRoutingMarkerIsNotOfferedAsAPlace(t *testing.T) {
	userID := seed(t, "Funchal", []string{"Mercado dos Lavradores"}, "nearme", "Funchal", "nearme")

	hint := quietPlaces().For(context.Background(), userID)

	if strings.Contains(strings.ToLower(hint), notACity) {
		t.Errorf("the routing marker reached the hint: %q", hint)
	}
	if !strings.Contains(hint, "Funchal") {
		t.Errorf("the real city was dropped along with it: %q", hint)
	}
}

// The hint window holds a couple of hundred tokens, so the trip somebody is
// planning now is worth more of it than one from a month ago.
func TestTheMostRecentCityComesFirst(t *testing.T) {
	userID := seed(t, "Porto", nil, "Braga", "Coimbra", "Porto")

	hint := quietPlaces().For(context.Background(), userID)

	first := strings.Index(hint, "Porto")
	if first < 0 {
		t.Fatalf("the most recent city is missing: %q", hint)
	}
	for _, older := range []string{"Coimbra", "Braga"} {
		if at := strings.Index(hint, older); at >= 0 && at < first {
			t.Errorf("%q came before the most recent city: %q", older, hint)
		}
	}
}

// A city discovered in conversation may never have become a city row. Its name
// is still worth offering.
func TestACityWithNoPlacesStillContributesItsName(t *testing.T) {
	userID := seed(t, "Funchal", []string{"Mercado dos Lavradores"}, "Esposende")

	hint := quietPlaces().For(context.Background(), userID)

	if !strings.Contains(hint, "Esposende") {
		t.Errorf("hint = %q, want it to carry the city with no places", hint)
	}
}

// A hint improves a transcript; it is not a precondition for one. A user with
// no history transcribes unhinted rather than being refused.
func TestAUserWithNoHistoryGetsNoHint(t *testing.T) {
	userID := seed(t, "Aveiro", []string{"Canal Central"})

	if hint := quietPlaces().For(context.Background(), userID); hint != "" {
		t.Errorf("hint = %q, want empty", hint)
	}
	if hint := quietPlaces().For(context.Background(), uuid.Nil); hint != "" {
		t.Errorf("hint for no user = %q, want empty", hint)
	}
}

// Failing to build a hint is no reason to refuse to listen.
func TestABrokenLookupIsSilentRatherThanFatal(t *testing.T) {
	broken := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if hint := broken.For(context.Background(), uuid.New()); hint != "" {
		t.Errorf("hint = %q, want empty", hint)
	}

	var absent *Places
	if hint := absent.For(context.Background(), uuid.New()); hint != "" {
		t.Errorf("hint from a nil Places = %q, want empty", hint)
	}
}
