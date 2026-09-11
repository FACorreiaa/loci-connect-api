package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// GenerationStore is the durable layer of the generation cache: one row per
// cached part of an answer in llm_generations (migration 0084).
//
// It is deliberately a separate interface from Repository. The chat service
// holds it as an optional dependency (nil means memory-only), and keeping it
// out of Repository means the existing mocks do not have to grow four methods
// they never call.
type GenerationStore interface {
	// GetGeneration returns the live row for cacheKey and counts the hit, or
	// (nil, nil) when there is no row or the row has expired.
	GetGeneration(ctx context.Context, cacheKey string) (*locitypes.LLMGeneration, error)
	// PutGeneration inserts the row, or replaces the response and its
	// provenance when the key already exists. CreatedAt is set by the store.
	PutGeneration(ctx context.Context, g locitypes.LLMGeneration) error
	// PurgeGenerationsByCity drops every row for a normalised city name (the
	// admin path after a bulk POI ingest) and returns how many went.
	PurgeGenerationsByCity(ctx context.Context, city string) (int64, error)
	// DeleteExpiredGenerations drops every row whose expires_at has passed
	// and returns how many went. Meant for a periodic sweep.
	DeleteExpiredGenerations(ctx context.Context) (int64, error)
}

var _ GenerationStore = (*RepositoryImpl)(nil)

const generationColumns = `cache_key, template_version, part, domain, model_id, model_version,
		       prompt_hash, city, city_id, response, packet_id, tokens_in, tokens_out,
		       created_at, expires_at, hit_count, last_hit_at`

// getGenerationQuery reads and counts the hit in one statement so a lookup is
// a single round trip and the counter cannot drift from what was served. The
// expiry check is in the WHERE so an expired row is invisible rather than
// served-then-deleted.
const getGenerationQuery = `
		UPDATE llm_generations
		SET hit_count = hit_count + 1, last_hit_at = NOW()
		WHERE cache_key = $1 AND expires_at > NOW()
		RETURNING ` + generationColumns

// putGenerationQuery upserts on cache_key. A conflict means the same inputs
// were generated again (the previous row expired, or a race between two
// misses); the newer answer wins and its counters start over. tokens_in /
// tokens_out are replaced because they describe this response, not the key.
const putGenerationQuery = `
		INSERT INTO llm_generations (
			cache_key, template_version, part, domain, model_id, model_version,
			prompt_hash, city, city_id, response, packet_id, tokens_in, tokens_out,
			expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (cache_key) DO UPDATE SET
			response = EXCLUDED.response,
			model_version = EXCLUDED.model_version,
			prompt_hash = EXCLUDED.prompt_hash,
			packet_id = EXCLUDED.packet_id,
			tokens_in = EXCLUDED.tokens_in,
			tokens_out = EXCLUDED.tokens_out,
			created_at = NOW(),
			expires_at = EXCLUDED.expires_at`

const purgeGenerationsByCityQuery = `DELETE FROM llm_generations WHERE city = $1`

const deleteExpiredGenerationsQuery = `DELETE FROM llm_generations WHERE expires_at <= NOW()`

func (r *RepositoryImpl) GetGeneration(ctx context.Context, cacheKey string) (*locitypes.LLMGeneration, error) {
	ctx, span := otel.Tracer("LlmInteractionRepo").Start(ctx, "GetGeneration", trace.WithAttributes(
		semconv.DBSystemKey.String(semconv.DBSystemPostgreSQL.Value.AsString()),
		attribute.String("db.operation", "UPDATE"),
		attribute.String("db.sql.table", "llm_generations"),
		attribute.String("loci.cache_key", cacheKey),
	))
	defer span.End()

	if cacheKey == "" {
		return nil, nil
	}

	var g locitypes.LLMGeneration
	err := r.pgpool.QueryRow(ctx, getGenerationQuery, cacheKey).Scan(
		&g.CacheKey, &g.TemplateVersion, &g.Part, &g.Domain, &g.ModelID, &g.ModelVersion,
		&g.PromptHash, &g.City, &g.CityID, &g.Response, &g.PacketID, &g.TokensIn, &g.TokensOut,
		&g.CreatedAt, &g.ExpiresAt, &g.HitCount, &g.LastHitAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			span.SetAttributes(attribute.Bool("loci.cache_hit", false))
			return nil, nil
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to read llm_generation")
		return nil, fmt.Errorf("failed to read llm_generation: %w", err)
	}
	span.SetAttributes(
		attribute.Bool("loci.cache_hit", true),
		attribute.String("loci.part", g.Part),
		attribute.Int("loci.hit_count", g.HitCount),
	)
	return &g, nil
}

func (r *RepositoryImpl) PutGeneration(ctx context.Context, g locitypes.LLMGeneration) error {
	ctx, span := otel.Tracer("LlmInteractionRepo").Start(ctx, "PutGeneration", trace.WithAttributes(
		semconv.DBSystemKey.String(semconv.DBSystemPostgreSQL.Value.AsString()),
		attribute.String("db.operation", "INSERT"),
		attribute.String("db.sql.table", "llm_generations"),
		attribute.String("loci.cache_key", g.CacheKey),
		attribute.String("loci.part", g.Part),
	))
	defer span.End()

	switch {
	case g.CacheKey == "":
		return errors.New("llm_generation: cache_key is required")
	case g.Part == "":
		return errors.New("llm_generation: part is required")
	case g.Response == "":
		return errors.New("llm_generation: response is empty")
	case g.ExpiresAt.IsZero():
		return errors.New("llm_generation: expires_at is required")
	}
	packetID := g.PacketID
	if packetID == "" {
		packetID = "ungrounded"
	}

	_, err := r.pgpool.Exec(ctx, putGenerationQuery,
		g.CacheKey, g.TemplateVersion, g.Part, g.Domain, g.ModelID, g.ModelVersion,
		g.PromptHash, g.City, g.CityID, g.Response, packetID, g.TokensIn, g.TokensOut,
		g.ExpiresAt,
	)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to upsert llm_generation")
		return fmt.Errorf("failed to upsert llm_generation: %w", err)
	}
	return nil
}

func (r *RepositoryImpl) PurgeGenerationsByCity(ctx context.Context, city string) (int64, error) {
	ctx, span := otel.Tracer("LlmInteractionRepo").Start(ctx, "PurgeGenerationsByCity", trace.WithAttributes(
		semconv.DBSystemKey.String(semconv.DBSystemPostgreSQL.Value.AsString()),
		attribute.String("db.operation", "DELETE"),
		attribute.String("db.sql.table", "llm_generations"),
		attribute.String("loci.city", city),
	))
	defer span.End()

	if city == "" {
		return 0, errors.New("llm_generation: city is required")
	}

	tag, err := r.pgpool.Exec(ctx, purgeGenerationsByCityQuery, city)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to purge llm_generations by city")
		return 0, fmt.Errorf("failed to purge llm_generations for city %q: %w", city, err)
	}
	span.SetAttributes(attribute.Int64("db.rows_affected", tag.RowsAffected()))
	return tag.RowsAffected(), nil
}

func (r *RepositoryImpl) DeleteExpiredGenerations(ctx context.Context) (int64, error) {
	ctx, span := otel.Tracer("LlmInteractionRepo").Start(ctx, "DeleteExpiredGenerations", trace.WithAttributes(
		semconv.DBSystemKey.String(semconv.DBSystemPostgreSQL.Value.AsString()),
		attribute.String("db.operation", "DELETE"),
		attribute.String("db.sql.table", "llm_generations"),
	))
	defer span.End()

	tag, err := r.pgpool.Exec(ctx, deleteExpiredGenerationsQuery)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to delete expired llm_generations")
		return 0, fmt.Errorf("failed to delete expired llm_generations: %w", err)
	}
	span.SetAttributes(attribute.Int64("db.rows_affected", tag.RowsAffected()))
	return tag.RowsAffected(), nil
}
