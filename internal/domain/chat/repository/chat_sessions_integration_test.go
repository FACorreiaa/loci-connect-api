//go:build integration

package repository

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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
