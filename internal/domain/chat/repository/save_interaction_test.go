package repository

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// The audit row has carried cache_key / cache_hit / prompt_hash / provider
// columns since migration 0043, and a city_id column since the table was
// created, without anyone writing any of them. With a durable
// generation cache the row is how "what did we show and where did it come
// from" gets answered, so every one of them is asserted here, in order.
func TestSaveInteraction_WritesCacheAndProvenanceColumns(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	sessionID := uuid.New()
	interactionID := uuid.New()
	cityID := uuid.New()
	payload := json.RawMessage(`{"parts":{"itinerary":{"served_from":"db"}}}`)

	interaction := locitypes.LlmInteraction{
		UserID:           userID,
		SessionID:        sessionID,
		Prompt:           "Unified Chat Stream - Domain: itinerary, Message: 3 days in Funchal",
		ResponseText:     "[itinerary]\n{}",
		ModelUsed:        "deepseek/deepseek-v4-flash",
		LatencyMs:        1234,
		CacheKey:         "abc123",
		CacheHit:         true,
		PromptHash:       "sha256-of-prompt",
		Provider:         "openrouter",
		PromptTokens:     10,
		CompletionTokens: 20,
		TotalTokens:      30,
		IsStreaming:      true,
		ResponsePayload:  payload,
		Intent:           "itinerary",
		SearchType:       "city_data,itinerary",
		CityID:           &cityID,
	}

	cacheKey, promptHash, provider := "abc123", "sha256-of-prompt", "openrouter"
	intent, searchType := "itinerary", "city_data,itinerary"

	mock.ExpectBeginTx(pgx.TxOptions{})
	mock.ExpectQuery(regexp.QuoteMeta(saveInteractionQuery)).
		WithArgs(
			userID, sessionID, interaction.Prompt, interaction.ResponseText, interaction.ModelUsed, 1234, "",
			&cacheKey, true, &promptHash, &provider,
			10, 20, 30,
			true, []byte(payload),
			&intent, &searchType, &cityID,
		).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(interactionID))
	mock.ExpectCommit()

	repo := NewRepositoryImpl(mock, slog.Default())
	got, err := repo.SaveInteraction(context.Background(), interaction)
	if err != nil {
		t.Fatalf("SaveInteraction: %v", err)
	}
	if got != interactionID {
		t.Fatalf("returned id %s, want %s", got, interactionID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// A caller that has not filled the new fields (there are several outside the
// unified stream) must keep producing the row it always did: the nullable
// text columns stay NULL, provider keeps its column default via COALESCE, and
// an empty response_payload is NULL rather than invalid JSONB.
//
// A NULL intent is load-bearing rather than incidental: it is how the recents
// activity feed tells an internal model call apart from something a person
// asked for. A caller outside the unified stream writing an intent would put
// its lookups on somebody's activity page.
func TestSaveInteraction_UnsetFieldsAreNull(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	interactionID := uuid.New()
	interaction := locitypes.LlmInteraction{
		UserID:       userID,
		Prompt:       "hello",
		ResponseText: "world",
		ModelUsed:    "m",
		CityName:     "Atlantis",
	}

	mock.ExpectBeginTx(pgx.TxOptions{})
	mock.ExpectQuery(regexp.QuoteMeta(saveInteractionQuery)).
		WithArgs(
			userID, uuid.Nil, "hello", "world", "m", 0, "Atlantis",
			(*string)(nil), false, (*string)(nil), (*string)(nil),
			0, 0, 0,
			false, []byte(nil),
			(*string)(nil), (*string)(nil), (*uuid.UUID)(nil),
		).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(interactionID))
	// A city name that is not in the table is a warning, not a failure.
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM cities WHERE name = $1 LIMIT 1`)).
		WithArgs("Atlantis").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectCommit()

	repo := NewRepositoryImpl(mock, slog.Default())
	got, err := repo.SaveInteraction(context.Background(), interaction)
	if err != nil {
		t.Fatalf("SaveInteraction: %v", err)
	}
	if got != interactionID {
		t.Fatalf("returned id %s, want %s", got, interactionID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
