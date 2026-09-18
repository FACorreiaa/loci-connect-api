//go:build integration

package recents

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

var testDB *pgxpool.Pool

func TestMain(m *testing.M) {
	testDB = testsupport.MustPool()
	os.Exit(m.Run())
}

func quietRepo() *RepositoryImpl {
	return NewRepository(testDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func newUser(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := testDB.Exec(context.Background(),
		"INSERT INTO users (id, email) VALUES ($1, $2)", id, id.String()+"@example.test")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

func insertPrompt(t *testing.T, userID uuid.UUID, prompt string, intent *string, city string, at time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := testDB.Exec(context.Background(), `
		INSERT INTO llm_interactions (id, user_id, session_id, prompt, city_name, intent, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, userID, uuid.New(), prompt, city, intent, at)
	if err != nil {
		t.Fatalf("insert llm_interaction: %v", err)
	}
	return id
}

func insertSavedItinerary(t *testing.T, userID uuid.UUID, title string, at time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := testDB.Exec(context.Background(), `
		INSERT INTO user_saved_itineraries (id, user_id, title, markdown_content, created_at)
		VALUES ($1, $2, $3, '# trip', $4)`, id, userID, title, at)
	if err != nil {
		t.Fatalf("insert user_saved_itineraries: %v", err)
	}
	return id
}

func insertFavourite(t *testing.T, userID uuid.UUID, name, contentType string, at time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := testDB.Exec(context.Background(), `
		INSERT INTO user_favorites (id, user_id, item_id, item_name, content_type, city_name, added_at)
		VALUES ($1, $2, $3, $4, $5, 'Porto', $6)`,
		id, userID, uuid.New().String(), name, contentType, at)
	if err != nil {
		t.Fatalf("insert user_favorites: %v", err)
	}
	return id
}

func truncate(t *testing.T) {
	t.Helper()
	testsupport.Truncate(t, testDB, "user_favorites", "user_saved_itineraries", "llm_interactions", "users")
}

func labels(entries []locitypes.ActivityEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Label)
	}
	return out
}

// The feed has to merge three tables into one timeline. Before it existed the
// recents page could only group llm_interactions by city, so a saved itinerary
// and a favourite were invisible and a chat turn was indistinguishable from a
// hotel search.
func TestActivityFeed_MergesThreeSourcesNewestFirst(t *testing.T) {
	truncate(t)
	user := newUser(t)
	base := time.Now().UTC().Truncate(time.Second)

	insertPrompt(t, user, "Unified Chat Stream - Domain: itinerary, Message: Three days in Porto", nil, "Porto", base.Add(-4*time.Hour))
	insertSavedItinerary(t, user, "Porto weekend", base.Add(-3*time.Hour))
	insertFavourite(t, user, "Livraria Lello", "poi", base.Add(-2*time.Hour))
	insertPrompt(t, user, "Unified Chat Stream - Domain: dining, Message: seafood in Cascais", strPtrLocal("dining"), "Cascais", base.Add(-1*time.Hour))

	entries, err := quietRepo().GetUserActivityFeed(context.Background(), user, 10, 0, locitypes.ActivityFeedFilter{})
	if err != nil {
		t.Fatalf("GetUserActivityFeed: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("want 4 entries, got %d: %v", len(entries), labels(entries))
	}

	wantKinds := []locitypes.ActivityKind{
		locitypes.ActivityKindPrompt,
		locitypes.ActivityKindFavourite,
		locitypes.ActivityKindSavedItinerary,
		locitypes.ActivityKindPrompt,
	}
	for i, want := range wantKinds {
		if entries[i].Kind != want {
			t.Errorf("entry %d: want kind %q, got %q (%s)", i, want, entries[i].Kind, entries[i].Label)
		}
	}

	if entries[0].Detail != "dining" {
		t.Errorf("newest entry should carry its written intent, got %q", entries[0].Detail)
	}
	if entries[1].Detail != "poi" {
		t.Errorf("favourite should carry its content type, got %q", entries[1].Detail)
	}
	if entries[2].Detail != "itinerary" {
		t.Errorf("saved itinerary detail, got %q", entries[2].Detail)
	}
}

// Rows written before the intent column started being populated still have to
// be typed, or the feed is blank on the day it ships. The domain is recovered
// from the prompt prefix the chat service generates.
func TestActivityFeed_TypesRowsWithNoIntentFromThePromptPrefix(t *testing.T) {
	truncate(t)
	user := newUser(t)
	now := time.Now().UTC()

	insertPrompt(t, user, "Unified Chat Stream - Domain: activities, Message: kayaking near Sesimbra", nil, "Sesimbra", now)

	entries, err := quietRepo().GetUserActivityFeed(context.Background(), user, 10, 0, locitypes.ActivityFeedFilter{})
	if err != nil {
		t.Fatalf("GetUserActivityFeed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if entries[0].Detail != "activities" {
		t.Errorf("want detail recovered as %q, got %q", "activities", entries[0].Detail)
	}
}

// llm_interactions also holds the chat service's own model calls — POI detail
// lookups nobody asked for. They are not activities and must not reach the
// feed. The allowlist is the generated prompt prefix, so a new internal prompt
// is excluded without anyone remembering to exclude it.
func TestActivityFeed_ExcludesInternalModelCalls(t *testing.T) {
	truncate(t)
	user := newUser(t)
	now := time.Now().UTC()

	insertPrompt(t, user, `Return ONLY a JSON object for "Livraria Lello" in Porto.`, nil, "Porto", now)
	insertPrompt(t, user, "Unified Chat Stream - Domain: general, Message: what should I do in Porto", nil, "Porto", now.Add(-time.Minute))

	entries, err := quietRepo().GetUserActivityFeed(context.Background(), user, 10, 0, locitypes.ActivityFeedFilter{})
	if err != nil {
		t.Fatalf("GetUserActivityFeed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want only the user-facing row, got %d: %v", len(entries), labels(entries))
	}
	if entries[0].Detail != "general" {
		t.Errorf("want general, got %q", entries[0].Detail)
	}
}

// Favourites saved in one batch share added_at to the microsecond. Ordering by
// timestamp alone lets those ties reorder between page fetches, which shows one
// row twice and drops another entirely. The id tiebreaker is what stops it.
func TestActivityFeed_PagesWithoutOverlapAcrossTiedTimestamps(t *testing.T) {
	truncate(t)
	user := newUser(t)
	tied := time.Now().UTC().Truncate(time.Second)

	for i := 0; i < 6; i++ {
		insertFavourite(t, user, "tied", "poi", tied)
	}

	repo := quietRepo()
	page1, err := repo.GetUserActivityFeed(context.Background(), user, 3, 0, locitypes.ActivityFeedFilter{})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	page2, err := repo.GetUserActivityFeed(context.Background(), user, 3, 3, locitypes.ActivityFeedFilter{})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}

	seen := map[string]bool{}
	for _, e := range append(append([]locitypes.ActivityEntry{}, page1...), page2...) {
		if seen[e.ID] {
			t.Fatalf("id %s appeared on both pages", e.ID)
		}
		seen[e.ID] = true
	}
	if len(seen) != 6 {
		t.Fatalf("want 6 distinct rows across two pages, got %d", len(seen))
	}
}

// The type chips narrow by kind and by detail, and the two are independent: an
// "Itineraries" chip means itinerary searches, a "Saved" chip means kept trips,
// and they must not return each other.
func TestActivityFeed_FiltersByKindAndDetail(t *testing.T) {
	truncate(t)
	user := newUser(t)
	now := time.Now().UTC()

	insertPrompt(t, user, "Unified Chat Stream - Domain: itinerary, Message: two days in Braga", nil, "Braga", now)
	insertSavedItinerary(t, user, "Braga kept", now.Add(-time.Minute))
	insertFavourite(t, user, "Bom Jesus", "poi", now.Add(-2*time.Minute))

	repo := quietRepo()

	onlyPrompts, err := repo.GetUserActivityFeed(context.Background(), user, 10, 0,
		locitypes.ActivityFeedFilter{Kinds: []string{"prompt"}, Details: []string{"itinerary"}})
	if err != nil {
		t.Fatalf("filtered: %v", err)
	}
	if len(onlyPrompts) != 1 || onlyPrompts[0].Kind != locitypes.ActivityKindPrompt {
		t.Fatalf("want one itinerary prompt, got %v", labels(onlyPrompts))
	}

	onlySaved, err := repo.GetUserActivityFeed(context.Background(), user, 10, 0,
		locitypes.ActivityFeedFilter{Kinds: []string{"saved_itinerary"}})
	if err != nil {
		t.Fatalf("saved: %v", err)
	}
	if len(onlySaved) != 1 || onlySaved[0].Label != "Braga kept" {
		t.Fatalf("want the kept trip, got %v", labels(onlySaved))
	}

	searched, err := repo.GetUserActivityFeed(context.Background(), user, 10, 0,
		locitypes.ActivityFeedFilter{Search: "Bom"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(searched) != 1 || searched[0].Label != "Bom Jesus" {
		t.Fatalf("want the favourite, got %v", labels(searched))
	}
}

// One user must never see another's history.
func TestActivityFeed_IsScopedToTheUser(t *testing.T) {
	truncate(t)
	mine := newUser(t)
	theirs := newUser(t)
	now := time.Now().UTC()

	insertPrompt(t, mine, "Unified Chat Stream - Domain: general, Message: mine", nil, "Lisbon", now)
	insertPrompt(t, theirs, "Unified Chat Stream - Domain: general, Message: theirs", nil, "Lisbon", now)
	insertFavourite(t, theirs, "theirs too", "poi", now)

	entries, err := quietRepo().GetUserActivityFeed(context.Background(), mine, 10, 0, locitypes.ActivityFeedFilter{})
	if err != nil {
		t.Fatalf("GetUserActivityFeed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want only my row, got %v", labels(entries))
	}
}

func strPtrLocal(s string) *string { return &s }
