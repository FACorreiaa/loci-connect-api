package repository

import (
	"context"
	"io"
	"log/slog"
	"regexp"
	"strings"
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

// The response persistResults stores is "[part]\n{…}\n\n" per part, in map
// order. Each part must be parsed on its own: CleanJSON only strips the first
// header, and a blob parsed whole failed at the next "[…]".
func TestParsePOIsFromResponse_MultiPartSections(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	blob := "[city_data]\n{\"general_city_data\":{\"city\":\"Madrid\",\"country\":\"Spain\"}}\n\n" +
		"[general_pois]\n{\"points_of_interest\":[{\"name\":\"Prado\",\"category\":\"Museum\"},{\"name\":\"Retiro\",\"category\":\"Park\"}]}\n\n" +
		"[hotels]\n{\"hotels\":[{\"name\":\"Hotel Urban\",\"category\":\"Hotel\"}]}\n\n"
	pois, err := parsePOIsFromResponse(blob, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	names := make([]string, 0, len(pois))
	for _, p := range pois {
		names = append(names, p.Name)
	}
	want := []string{"Prado", "Retiro", "Hotel Urban"}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v, want %v", names, want)
		}
	}
}

// Places serialised one after another without the array brackets
// ("{…},{…}") are still a list.
func TestParsePOIsFromResponse_CommaJoinedPlaces(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	blob := `{"id":"4612681a-be9a-4caa-aee6-195faf003fd4","name":"Miradouro de Santa Luzia","category":"Viewpoint"},` +
		`{"id":"5a4b1c2d-0000-4000-8000-000000000001","name":"Time Out Market","category":"Market"}`
	pois, err := parsePOIsFromResponse(blob, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pois) != 2 || pois[0].Name != "Miradouro de Santa Luzia" || pois[1].Name != "Time Out Market" {
		t.Fatalf("got %+v", pois)
	}
}

// The status line stored when no part produced anything is not JSON and not
// an error: no places, no warning.
func TestParsePOIsFromResponse_PlainTextIsQuiet(t *testing.T) {
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	pois, err := parsePOIsFromResponse("Processed nearby request for Rome", logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pois) != 0 {
		t.Fatalf("expected no places, got %+v", pois)
	}
	if strings.Contains(buf.String(), "Could not parse") {
		t.Fatalf("plain text logged a WARN: %s", buf.String())
	}
}

// The prompt wrapper reads "Unified Chat Stream - Domain: …"; the old marker
// ("Unified Chat - Domain: …") never matched it, so hotel lists were parsed
// into itinerary_pois.
func TestDomainListPromptRE(t *testing.T) {
	for _, prompt := range []string{
		"Unified Chat Stream - Domain: accommodation, Message: hotels in Madrid",
		"Unified Chat Stream - Domain: dining, Message: dinner",
		"Unified Chat - Domain: activities",
	} {
		if !domainListPromptRE.MatchString(prompt) {
			t.Errorf("%q should be a domain list prompt", prompt)
		}
	}
	for _, prompt := range []string{
		"Unified Chat Stream - Domain: itinerary, Message: 3 days in Rome",
		"Unified Chat Stream - Domain: general, Message: what to see",
	} {
		if domainListPromptRE.MatchString(prompt) {
			t.Errorf("%q should not be a domain list prompt", prompt)
		}
	}
}
