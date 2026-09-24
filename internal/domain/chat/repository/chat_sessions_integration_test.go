//go:build integration

package repository

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

var testChatDB *pgxpool.Pool

func TestMain(m *testing.M) {
	testChatDB = testsupport.MustPool()
	os.Exit(m.Run())
}

func newTestUser(t *testing.T, email string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := testChatDB.Exec(context.Background(),
		"INSERT INTO users (id, email) VALUES ($1, $2)", id, email)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

// GetUserChatSessions groups interactions by a synthesised session_key. When an
// interaction has neither a session_id nor a city_name, the fallback
// `city_name || '_' || DATE(created_at)` used to evaluate to NULL (SQL concat
// propagates NULL), the scan into a non-pointer string failed, and the whole
// chat history 500'd with:
//
//	can't scan into dest[0] (col: session_key): cannot scan NULL into *string
//
// This test pins the NULL/NULL row down.
func TestGetUserChatSessions_NullSessionIDAndCityName(t *testing.T) {
	testsupport.Truncate(t, testChatDB, "llm_interactions", "users")

	userID := newTestUser(t, "null-session-key@loci.test")

	// The row that used to break the query: no session_id, no city_name.
	_, err := testChatDB.Exec(context.Background(),
		`INSERT INTO llm_interactions (user_id, session_id, city_name, prompt, response)
		 VALUES ($1, NULL, NULL, $2, $3)`,
		userID, "where should I go this weekend?", "try Évora")
	if err != nil {
		t.Fatalf("insert interaction: %v", err)
	}

	// A well-formed row alongside it, so we also prove grouping still works.
	_, err = testChatDB.Exec(context.Background(),
		`INSERT INTO llm_interactions (user_id, session_id, city_name, prompt, response)
		 VALUES ($1, $2, $3, $4, $5)`,
		userID, uuid.New(), "Porto", "what's open late?", "several places")
	if err != nil {
		t.Fatalf("insert interaction: %v", err)
	}

	repo := NewRepositoryImpl(testChatDB, slog.New(slog.NewTextHandler(io.Discard, nil)))

	got, err := repo.GetUserChatSessions(context.Background(), userID, 1, 25)
	if err != nil {
		t.Fatalf("GetUserChatSessions returned error: %v", err)
	}

	if len(got.Sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(got.Sessions))
	}
	if got.Total != 2 {
		t.Fatalf("expected total 2, got %d", got.Total)
	}

	// The NULL-city row must come back with an empty (not failed) city name.
	var sawEmptyCity, sawPorto bool
	for _, s := range got.Sessions {
		switch s.CityName {
		case "":
			sawEmptyCity = true
		case "Porto":
			sawPorto = true
		}
	}
	if !sawEmptyCity || !sawPorto {
		t.Fatalf("expected both a NULL-city and a Porto session, got %+v", got.Sessions)
	}
}

// A multi-city trip files its later cities as children of the first city's
// session; the sessions list shows the trip once, under that session.
func TestGetUserChatSessions_HidesMultiCityChildSessions(t *testing.T) {
	testsupport.Truncate(t, testChatDB, "llm_interactions", "chat_sessions", "users")

	userID := newTestUser(t, "multi-city-children@loci.test")
	repo := NewRepositoryImpl(testChatDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Now()

	parent := locitypes.ChatSession{ID: uuid.New(), UserID: userID, CityName: "Milan", CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour), Status: "active"}
	if err := repo.CreateSession(context.Background(), parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child := locitypes.ChatSession{ID: uuid.New(), UserID: userID, CityName: "Rome", CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour), Status: "active", ParentSessionID: &parent.ID}
	if err := repo.CreateSession(context.Background(), child); err != nil {
		t.Fatalf("create child: %v", err)
	}
	for _, s := range []locitypes.ChatSession{parent, child} {
		if _, err := testChatDB.Exec(context.Background(),
			`INSERT INTO llm_interactions (user_id, session_id, city_name, prompt, response) VALUES ($1, $2, $3, $4, $5)`,
			userID, s.ID, s.CityName, "Milan, Rome and Florence in a week", "planned"); err != nil {
			t.Fatalf("insert interaction: %v", err)
		}
	}

	got, err := repo.GetUserChatSessions(context.Background(), userID, 1, 25)
	if err != nil {
		t.Fatalf("GetUserChatSessions: %v", err)
	}
	if len(got.Sessions) != 1 || got.Total != 1 {
		t.Fatalf("expected the trip once, got %d sessions (total %d): %+v", len(got.Sessions), got.Total, got.Sessions)
	}
	if got.Sessions[0].CityName != "Milan" {
		t.Fatalf("the trip is listed under its first city, got %q", got.Sessions[0].CityName)
	}

	recent, err := repo.GetRecentChatSessions(context.Background(), userID, 10)
	if err != nil {
		t.Fatalf("GetRecentChatSessions: %v", err)
	}
	if len(recent) != 1 || recent[0].ID != parent.ID {
		t.Fatalf("recent sessions must hide the child, got %+v", recent)
	}
}
