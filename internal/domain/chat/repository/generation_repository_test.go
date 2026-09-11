package repository

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func newGenerationMock(t *testing.T) (pgxmock.PgxPoolIface, *RepositoryImpl) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	t.Cleanup(mock.Close)
	return mock, NewRepositoryImpl(mock, slog.Default())
}

func generationRows() *pgxmock.Rows {
	return pgxmock.NewRows([]string{
		"cache_key", "template_version", "part", "domain", "model_id", "model_version",
		"prompt_hash", "city", "city_id", "response", "packet_id", "tokens_in", "tokens_out",
		"created_at", "expires_at", "hit_count", "last_hit_at",
	})
}

// A lookup is one statement: the UPDATE that counts the hit is also the read,
// and the expiry check is in its WHERE, so the store never has to choose
// between serving and deleting an expired row.
func TestGetGeneration_HitReturnsRowAndCountsIt(t *testing.T) {
	mock, repo := newGenerationMock(t)

	cityID := uuid.New()
	now := time.Now()
	lastHit := now.Add(-time.Minute)

	mock.ExpectQuery(regexp.QuoteMeta(getGenerationQuery)).
		WithArgs("gen:abc").
		WillReturnRows(generationRows().AddRow(
			"gen:abc", "v2", "itinerary", "itinerary", "deepseek/deepseek-v4-flash", "v4-flash-2026",
			"sha", "funchal", &cityID, `{"itinerary_name":"x"}`, "pkt", 100, 2000,
			now, now.Add(14*24*time.Hour), 3, &lastHit,
		))

	got, err := repo.GetGeneration(context.Background(), "gen:abc")
	if err != nil {
		t.Fatalf("GetGeneration: %v", err)
	}
	if got == nil {
		t.Fatal("GetGeneration returned nil for a live row")
	}
	if got.CacheKey != "gen:abc" || got.Part != "itinerary" || got.Response != `{"itinerary_name":"x"}` {
		t.Fatalf("unexpected row: %+v", got)
	}
	if got.CityID == nil || *got.CityID != cityID {
		t.Fatalf("city_id not scanned: %+v", got.CityID)
	}
	if got.HitCount != 3 || got.LastHitAt == nil || !got.LastHitAt.Equal(lastHit) {
		t.Fatalf("hit bookkeeping not scanned: %+v", got)
	}
	if got.TokensIn != 100 || got.TokensOut != 2000 {
		t.Fatalf("token counts not scanned: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetGeneration_NullableColumnsScan(t *testing.T) {
	mock, repo := newGenerationMock(t)

	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(getGenerationQuery)).
		WithArgs("gen:city").
		WillReturnRows(generationRows().AddRow(
			"gen:city", "v2", "city_data", "general", "m", "",
			"", "porto", (*uuid.UUID)(nil), "text", "ungrounded", 0, 0,
			now, now.Add(time.Hour), 1, (*time.Time)(nil),
		))

	got, err := repo.GetGeneration(context.Background(), "gen:city")
	if err != nil {
		t.Fatalf("GetGeneration: %v", err)
	}
	if got.CityID != nil || got.LastHitAt != nil {
		t.Fatalf("expected nil city_id and last_hit_at, got %+v %+v", got.CityID, got.LastHitAt)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// No row (missing or expired) is a miss, not an error: the caller falls
// through to the provider and must not log a failure for the common case.
func TestGetGeneration_MissIsNilNil(t *testing.T) {
	mock, repo := newGenerationMock(t)

	mock.ExpectQuery(regexp.QuoteMeta(getGenerationQuery)).
		WithArgs("gen:missing").
		WillReturnRows(generationRows())

	got, err := repo.GetGeneration(context.Background(), "gen:missing")
	if err != nil {
		t.Fatalf("GetGeneration returned error on miss: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil on miss, got %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetGeneration_EmptyKeySkipsTheDatabase(t *testing.T) {
	mock, repo := newGenerationMock(t)

	got, err := repo.GetGeneration(context.Background(), "")
	if err != nil || got != nil {
		t.Fatalf("expected (nil, nil) for empty key, got (%+v, %v)", got, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetGeneration_DatabaseErrorIsReturned(t *testing.T) {
	mock, repo := newGenerationMock(t)

	boom := errors.New("connection reset")
	mock.ExpectQuery(regexp.QuoteMeta(getGenerationQuery)).
		WithArgs("gen:abc").
		WillReturnError(boom)

	got, err := repo.GetGeneration(context.Background(), "gen:abc")
	if !errors.Is(err, boom) {
		t.Fatalf("expected wrapped db error, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil row on error, got %+v", got)
	}
}

func TestPutGeneration_UpsertsEveryColumn(t *testing.T) {
	mock, repo := newGenerationMock(t)

	cityID := uuid.New()
	expires := time.Now().Add(7 * 24 * time.Hour)
	g := locitypes.LLMGeneration{
		CacheKey:        "gen:abc",
		TemplateVersion: "v2",
		Part:            "hotels",
		Domain:          "accommodation",
		ModelID:         "model-planned",
		ModelVersion:    "model-answered",
		PromptHash:      "phash",
		City:            "lisbon",
		CityID:          &cityID,
		Response:        `{"hotels":[]}`,
		PacketID:        "pkt-1",
		TokensIn:        12,
		TokensOut:       345,
		ExpiresAt:       expires,
	}

	mock.ExpectExec(regexp.QuoteMeta(putGenerationQuery)).
		WithArgs(
			"gen:abc", "v2", "hotels", "accommodation", "model-planned", "model-answered",
			"phash", "lisbon", &cityID, `{"hotels":[]}`, "pkt-1", 12, 345,
			expires,
		).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := repo.PutGeneration(context.Background(), g); err != nil {
		t.Fatalf("PutGeneration: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// An ungrounded generation has no packet; the row says so explicitly rather
// than carrying an empty string that reads like a missing value.
func TestPutGeneration_DefaultsPacketID(t *testing.T) {
	mock, repo := newGenerationMock(t)

	expires := time.Now().Add(time.Hour)
	mock.ExpectExec(regexp.QuoteMeta(putGenerationQuery)).
		WithArgs(
			"gen:x", "", "city_data", "", "", "",
			"", "", (*uuid.UUID)(nil), "text", "ungrounded", 0, 0,
			expires,
		).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err := repo.PutGeneration(context.Background(), locitypes.LLMGeneration{
		CacheKey:  "gen:x",
		Part:      "city_data",
		Response:  "text",
		ExpiresAt: expires,
	})
	if err != nil {
		t.Fatalf("PutGeneration: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// The table refuses empty responses with a CHECK; the store refuses them
// first so an unparseable or empty stream never even reaches the database.
func TestPutGeneration_RejectsIncompleteRows(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	cases := []struct {
		name string
		g    locitypes.LLMGeneration
	}{
		{"missing key", locitypes.LLMGeneration{Part: "itinerary", Response: "x", ExpiresAt: expires}},
		{"missing part", locitypes.LLMGeneration{CacheKey: "gen:k", Response: "x", ExpiresAt: expires}},
		{"empty response", locitypes.LLMGeneration{CacheKey: "gen:k", Part: "itinerary", ExpiresAt: expires}},
		{"zero expiry", locitypes.LLMGeneration{CacheKey: "gen:k", Part: "itinerary", Response: "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock, repo := newGenerationMock(t)
			if err := repo.PutGeneration(context.Background(), tc.g); err == nil {
				t.Fatal("expected validation error")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("database was touched: %v", err)
			}
		})
	}
}

func TestPurgeGenerationsByCity(t *testing.T) {
	mock, repo := newGenerationMock(t)

	mock.ExpectExec(regexp.QuoteMeta(purgeGenerationsByCityQuery)).
		WithArgs("funchal").
		WillReturnResult(pgxmock.NewResult("DELETE", 3))

	n, err := repo.PurgeGenerationsByCity(context.Background(), "funchal")
	if err != nil {
		t.Fatalf("PurgeGenerationsByCity: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 rows purged, got %d", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// An empty city would match the rows written before a city was resolved,
// which is every ungrounded turn; that is not what "purge a city" means.
func TestPurgeGenerationsByCity_RejectsEmptyCity(t *testing.T) {
	mock, repo := newGenerationMock(t)

	if _, err := repo.PurgeGenerationsByCity(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty city")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database was touched: %v", err)
	}
}

func TestDeleteExpiredGenerations(t *testing.T) {
	mock, repo := newGenerationMock(t)

	mock.ExpectExec(regexp.QuoteMeta(deleteExpiredGenerationsQuery)).
		WillReturnResult(pgxmock.NewResult("DELETE", 7))

	n, err := repo.DeleteExpiredGenerations(context.Background())
	if err != nil {
		t.Fatalf("DeleteExpiredGenerations: %v", err)
	}
	if n != 7 {
		t.Fatalf("expected 7 rows deleted, got %d", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
