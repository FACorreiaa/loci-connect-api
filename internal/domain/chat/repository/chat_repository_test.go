package repository

import (
	"context"
	"log/slog"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"
)

func TestGetInteractionByID(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	interactionID := uuid.New()
	userID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			id, user_id, prompt, response, model_name, latency_ms,
			prompt_tokens, completion_tokens, total_tokens,
			request_payload, response_payload
		FROM llm_interactions
		WHERE id = $1
	`)).
		WithArgs(interactionID).
		WillReturnRows(
			pgxmock.NewRows([]string{
				"id", "user_id", "prompt", "response", "model_name", "latency_ms",
				"prompt_tokens", "completion_tokens", "total_tokens",
				"request_payload", "response_payload",
			}).AddRow(
				interactionID, userID, "prompt", "response", "gpt-4", 123,
				10, 20, 30,
				[]byte(`{"req":true}`), []byte(`{"res":true}`),
			),
		)

	repo := NewRepositoryImpl(mock, slog.Default())

	got, err := repo.GetInteractionByID(context.Background(), interactionID)
	if err != nil {
		t.Fatalf("GetInteractionByID returned error: %v", err)
	}
	if got.ID != interactionID || got.UserID != userID {
		t.Fatalf("unexpected IDs: %s %s", got.ID, got.UserID)
	}
	if got.PromptTokens != 10 || got.TotalTokens != 30 {
		t.Fatalf("unexpected token counts: %+v", got)
	}
}

func TestGetSession(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	sessionID := uuid.New()
	userID := uuid.New()
	profileID := uuid.New()
	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT id, user_id, profile_id, city_name, current_itinerary, conversation_history, session_context,
               created_at, updated_at, expires_at, status
        FROM chat_sessions WHERE id = $1
    `)).
		WithArgs(sessionID).
		WillReturnRows(
			pgxmock.NewRows([]string{
				"id", "user_id", "profile_id", "city_name", "current_itinerary", "conversation_history", "session_context",
				"created_at", "updated_at", "expires_at", "status",
			}).AddRow(
				sessionID, userID, profileID, "Lisbon",
				[]byte(`{}`), []byte(`[{"role":"user","content":"hi"}]`), []byte(`{"city_name":"Lisbon"}`),
				now, now, now.Add(time.Hour), "active",
			),
		)

	repo := NewRepositoryImpl(mock, slog.Default())
	got, err := repo.GetSession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("GetSession returned error: %v", err)
	}
	if got.ID != sessionID || got.UserID != userID || got.ProfileID != profileID {
		t.Fatalf("unexpected session IDs: %+v", got)
	}
	if got.Status != "active" {
		t.Fatalf("unexpected status: %s", got.Status)
	}
	if got.CityName != "Lisbon" {
		t.Fatalf("unexpected city name: %s", got.CityName)
	}
}
