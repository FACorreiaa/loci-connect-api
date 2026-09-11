//go:build integration

package repository

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func newGenerationRepo(t *testing.T) *RepositoryImpl {
	t.Helper()
	testsupport.Truncate(t, testChatDB, "llm_generations")
	return NewRepositoryImpl(testChatDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func sampleGeneration(key, city string, ttl time.Duration) locitypes.LLMGeneration {
	return locitypes.LLMGeneration{
		CacheKey:        key,
		TemplateVersion: "v2",
		Part:            "itinerary",
		Domain:          "itinerary",
		ModelID:         "deepseek/deepseek-v4-flash",
		ModelVersion:    "v4-flash-2026-08",
		PromptHash:      "prompt-hash",
		City:            city,
		Response:        `{"itinerary_name":"Three days in ` + city + `"}`,
		PacketID:        "pkt-" + city,
		TokensIn:        1200,
		TokensOut:       3400,
		ExpiresAt:       time.Now().Add(ttl),
	}
}

func TestGenerationStore_PutThenGet(t *testing.T) {
	repo := newGenerationRepo(t)
	ctx := context.Background()

	cityID := uuid.New()
	g := sampleGeneration("gen:put-get", "funchal", time.Hour)
	g.CityID = &cityID
	if err := repo.PutGeneration(ctx, g); err != nil {
		t.Fatalf("PutGeneration: %v", err)
	}

	got, err := repo.GetGeneration(ctx, "gen:put-get")
	if err != nil {
		t.Fatalf("GetGeneration: %v", err)
	}
	if got == nil {
		t.Fatal("expected a hit")
	}
	if got.Response != g.Response || got.Part != g.Part || got.ModelID != g.ModelID ||
		got.ModelVersion != g.ModelVersion || got.PromptHash != g.PromptHash ||
		got.PacketID != g.PacketID || got.TokensIn != g.TokensIn || got.TokensOut != g.TokensOut ||
		got.City != g.City || got.TemplateVersion != g.TemplateVersion || got.Domain != g.Domain {
		t.Fatalf("round trip lost data:\n put %+v\n got %+v", g, *got)
	}
	if got.CityID == nil || *got.CityID != cityID {
		t.Fatalf("city_id round trip: %v", got.CityID)
	}
	if got.CreatedAt.IsZero() || got.ExpiresAt.Sub(g.ExpiresAt).Abs() > time.Second {
		t.Fatalf("timestamps: created=%v expires=%v want~%v", got.CreatedAt, got.ExpiresAt, g.ExpiresAt)
	}
}

// Every read counts. hit_count is the cheapest hit-rate signal the table can
// offer and last_hit_at is what a future LRU sweep would key on.
func TestGenerationStore_HitCountIncrements(t *testing.T) {
	repo := newGenerationRepo(t)
	ctx := context.Background()

	if err := repo.PutGeneration(ctx, sampleGeneration("gen:hits", "porto", time.Hour)); err != nil {
		t.Fatalf("PutGeneration: %v", err)
	}

	var lastHit *time.Time
	for want := 1; want <= 3; want++ {
		got, err := repo.GetGeneration(ctx, "gen:hits")
		if err != nil {
			t.Fatalf("GetGeneration #%d: %v", want, err)
		}
		if got == nil {
			t.Fatalf("GetGeneration #%d: miss", want)
		}
		if got.HitCount != want {
			t.Fatalf("hit_count after read %d = %d", want, got.HitCount)
		}
		if got.LastHitAt == nil {
			t.Fatalf("last_hit_at not set after read %d", want)
		}
		if lastHit != nil && got.LastHitAt.Before(*lastHit) {
			t.Fatalf("last_hit_at went backwards: %v then %v", *lastHit, *got.LastHitAt)
		}
		lastHit = got.LastHitAt
	}
}

func TestGenerationStore_ExpiredRowIsAMiss(t *testing.T) {
	repo := newGenerationRepo(t)
	ctx := context.Background()

	if err := repo.PutGeneration(ctx, sampleGeneration("gen:expired", "braga", -time.Minute)); err != nil {
		t.Fatalf("PutGeneration: %v", err)
	}

	got, err := repo.GetGeneration(ctx, "gen:expired")
	if err != nil {
		t.Fatalf("GetGeneration: %v", err)
	}
	if got != nil {
		t.Fatalf("expired row was served: %+v", *got)
	}

	// And the row is still there for the sweep, untouched by the miss.
	var hits int
	if err := testChatDB.QueryRow(ctx,
		`SELECT hit_count FROM llm_generations WHERE cache_key = $1`, "gen:expired").Scan(&hits); err != nil {
		t.Fatalf("row vanished on miss: %v", err)
	}
	if hits != 0 {
		t.Fatalf("a miss counted as a hit: hit_count=%d", hits)
	}
}

// A second generation for the same key replaces the answer and restarts the
// clock; it must not fail on the primary key and must not keep the old text.
func TestGenerationStore_PutUpsertsOnConflict(t *testing.T) {
	repo := newGenerationRepo(t)
	ctx := context.Background()

	first := sampleGeneration("gen:upsert", "coimbra", -time.Minute)
	if err := repo.PutGeneration(ctx, first); err != nil {
		t.Fatalf("first PutGeneration: %v", err)
	}
	if got, _ := repo.GetGeneration(ctx, "gen:upsert"); got != nil {
		t.Fatal("expired first row should be a miss")
	}

	second := first
	second.Response = `{"itinerary_name":"regenerated"}`
	second.ModelVersion = "v4-flash-2026-09"
	second.PacketID = "pkt-new"
	second.TokensOut = 99
	second.ExpiresAt = time.Now().Add(time.Hour)
	if err := repo.PutGeneration(ctx, second); err != nil {
		t.Fatalf("second PutGeneration: %v", err)
	}

	got, err := repo.GetGeneration(ctx, "gen:upsert")
	if err != nil {
		t.Fatalf("GetGeneration: %v", err)
	}
	if got == nil {
		t.Fatal("upserted row should be live")
	}
	if got.Response != second.Response || got.ModelVersion != second.ModelVersion ||
		got.PacketID != second.PacketID || got.TokensOut != second.TokensOut {
		t.Fatalf("upsert kept stale columns: %+v", *got)
	}
	if got.HitCount != 1 {
		t.Fatalf("hit_count after upsert + one read = %d, want 1", got.HitCount)
	}
}

func TestGenerationStore_PurgeByCity(t *testing.T) {
	repo := newGenerationRepo(t)
	ctx := context.Background()

	for _, g := range []locitypes.LLMGeneration{
		sampleGeneration("gen:f1", "funchal", time.Hour),
		sampleGeneration("gen:f2", "funchal", time.Hour),
		sampleGeneration("gen:p1", "porto", time.Hour),
	} {
		if err := repo.PutGeneration(ctx, g); err != nil {
			t.Fatalf("PutGeneration %s: %v", g.CacheKey, err)
		}
	}

	n, err := repo.PurgeGenerationsByCity(ctx, "funchal")
	if err != nil {
		t.Fatalf("PurgeGenerationsByCity: %v", err)
	}
	if n != 2 {
		t.Fatalf("purged %d rows, want 2", n)
	}
	if got, _ := repo.GetGeneration(ctx, "gen:f1"); got != nil {
		t.Fatal("funchal row survived the purge")
	}
	if got, _ := repo.GetGeneration(ctx, "gen:p1"); got == nil {
		t.Fatal("porto row was purged with funchal")
	}
}

func TestGenerationStore_DeleteExpired(t *testing.T) {
	repo := newGenerationRepo(t)
	ctx := context.Background()

	for _, g := range []locitypes.LLMGeneration{
		sampleGeneration("gen:old1", "lisbon", -time.Hour),
		sampleGeneration("gen:old2", "lisbon", -time.Second),
		sampleGeneration("gen:live", "lisbon", time.Hour),
	} {
		if err := repo.PutGeneration(ctx, g); err != nil {
			t.Fatalf("PutGeneration %s: %v", g.CacheKey, err)
		}
	}

	n, err := repo.DeleteExpiredGenerations(ctx)
	if err != nil {
		t.Fatalf("DeleteExpiredGenerations: %v", err)
	}
	if n != 2 {
		t.Fatalf("deleted %d rows, want 2", n)
	}
	var remaining int
	if err := testChatDB.QueryRow(ctx, `SELECT COUNT(*) FROM llm_generations`).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("%d rows remain, want 1", remaining)
	}
}

// The database refuses what the store refuses, so a direct write cannot
// smuggle an empty answer into the cache either.
func TestGenerationStore_EmptyResponseRejectedByTable(t *testing.T) {
	newGenerationRepo(t)
	_, err := testChatDB.Exec(context.Background(),
		`INSERT INTO llm_generations (cache_key, part, response, expires_at) VALUES ($1, $2, '', NOW() + INTERVAL '1 hour')`,
		"gen:empty", "city_data")
	if err == nil {
		t.Fatal("CHECK (response <> '') did not fire")
	}
}

// Migration 0084 rolls back and forward cleanly. MustPool has already set
// goose's base FS to the embedded migrations, so the directory name is the
// one RunMigrations uses.
func TestGenerationStore_MigrationDownUp(t *testing.T) {
	newGenerationRepo(t)

	sqlDB, err := sql.Open("pgx", testChatDB.Config().ConnString())
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer sqlDB.Close()

	if err := goose.DownTo(sqlDB, "migrations", 83); err != nil {
		t.Fatalf("goose.DownTo(83): %v", err)
	}
	var exists bool
	if err := testChatDB.QueryRow(context.Background(),
		`SELECT to_regclass('llm_generations') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("to_regclass: %v", err)
	}
	if exists {
		t.Fatal("llm_generations still exists after down")
	}

	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("goose.Up: %v", err)
	}
	if err := testChatDB.QueryRow(context.Background(),
		`SELECT to_regclass('llm_generations') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("to_regclass: %v", err)
	}
	if !exists {
		t.Fatal("llm_generations missing after up")
	}

	// The table is usable again, not just present.
	repo := NewRepositoryImpl(testChatDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := repo.PutGeneration(context.Background(), sampleGeneration("gen:after-up", "faro", time.Hour)); err != nil {
		t.Fatalf("PutGeneration after up: %v", err)
	}
}
