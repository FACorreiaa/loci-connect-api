package poi

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// LexicalHit is one result from the deterministic search lane, carrying the
// scores that produced it.
//
// The scores are returned rather than folded away because the caller fuses this
// lane with the vector lane and needs to know which lane found what. The
// pre-existing hybrid search computes similarity_score and hybrid_score in SQL
// and then discards both during row mapping, which is why nothing downstream has
// ever been able to explain a ranking.
type LexicalHit struct {
	POI locitypes.POIDetailedInfo
	// TextRank is ts_rank_cd over search_tsv; 0 when only the trigram arm matched.
	TextRank float64
	// NameSimilarity is pg_trgm similarity against the name; 0 when only the
	// full-text arm matched.
	NameSimilarity float64
	// ExactName reports a case-insensitive exact name match — the strongest
	// signal there is, and the one embeddings are worst at.
	ExactName bool
}

// maxLexicalQueryChars bounds the query before it reaches the database. Long
// junk queries produce large tsquery trees and pointless trigram scans.
const maxLexicalQueryChars = 512

// nameSimilarityFloor is the pg_trgm threshold for the typo arm. 0.3 is what
// city fuzzy-matching already uses (city_repository.FindCityByFuzzyName), kept
// identical so "close enough" means one thing across the system.
const nameSimilarityFloor = 0.3

type lexicalRow struct {
	ID             uuid.UUID `db:"id"`
	Name           string    `db:"name"`
	Description    string    `db:"description"`
	Category       string    `db:"category"`
	Address        string    `db:"address"`
	Longitude      float64   `db:"longitude"`
	Latitude       float64   `db:"latitude"`
	TextRank       float64   `db:"text_rank"`
	NameSimilarity float64   `db:"name_similarity"`
	ExactName      bool      `db:"exact_name"`
}

// SearchPOIsLexical runs deterministic full-text and trigram search over a
// city's POIs.
//
// Two arms, unioned:
//
//   - full-text over the weighted search_tsv column, via websearch_to_tsquery.
//     That parser is used deliberately: it accepts whatever a user types —
//     quotes, OR, minus signs, stray parentheses — and can never raise a syntax
//     error or let input change the query's structure. User text is a bound
//     parameter and is never concatenated into SQL or into a tsquery.
//   - trigram similarity on the name, which catches the misspellings that
//     tokenised search cannot.
//
// A row matching either arm is returned once, ranked by the stronger signal.
func (r *RepositoryImpl) SearchPOIsLexical(ctx context.Context, cityID uuid.UUID, query string, limit int) ([]LexicalHit, error) {
	ctx, span := otel.Tracer("Repository").Start(ctx, "SearchPOIsLexical", trace.WithAttributes(
		attribute.String("city.id", cityID.String()),
		attribute.Int("query.length", len(query)),
		attribute.Int("limit", limit),
	))
	defer span.End()

	l := r.logger.With(slog.String("method", "SearchPOIsLexical"))

	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		// An empty query is not an error, it simply matches nothing. Returning
		// an error here would force every caller into a guard clause.
		return nil, nil
	}
	if len(trimmed) > maxLexicalQueryChars {
		return nil, fmt.Errorf("search query must be at most %d characters; got %d",
			maxLexicalQueryChars, len(trimmed))
	}
	if limit <= 0 {
		limit = 20
	}

	const sql = `
		WITH q AS (
			SELECT websearch_to_tsquery('english', $2) AS tsq
		)
		SELECT
			p.id,
			p.name,
			COALESCE(p.description, '') AS description,
			COALESCE(p.category, COALESCE(p.poi_type, '')) AS category,
			COALESCE(p.address, '') AS address,
			ST_X(p.location::geometry) AS longitude,
			ST_Y(p.location::geometry) AS latitude,
			COALESCE(ts_rank_cd(p.search_tsv, q.tsq), 0)::float8 AS text_rank,
			COALESCE(similarity(p.name, $2), 0)::float8 AS name_similarity,
			(lower(btrim(p.name)) = lower(btrim($2))) AS exact_name
		FROM points_of_interest p
		CROSS JOIN q
		WHERE ($1::uuid IS NULL OR p.city_id = $1)
		  AND (
			p.search_tsv @@ q.tsq
			OR similarity(p.name, $2) > $3
		  )
		ORDER BY
			exact_name DESC,
			text_rank DESC,
			name_similarity DESC,
			p.name
		LIMIT $4`

	var cityArg any
	if cityID != uuid.Nil {
		cityArg = cityID
	}

	rows, err := r.pgpool.Query(ctx, sql, cityArg, trimmed, nameSimilarityFloor, limit)
	if err != nil {
		l.ErrorContext(ctx, "Failed to execute lexical search", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database query failed")
		return nil, fmt.Errorf("failed to execute lexical POI search: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[lexicalRow])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect lexical search rows", slog.Any("error", err))
		span.RecordError(err)
		return nil, fmt.Errorf("failed to collect lexical search rows: %w", err)
	}

	hits := make([]LexicalHit, len(dbRows))
	for i, row := range dbRows {
		hits[i] = LexicalHit{
			POI: locitypes.POIDetailedInfo{
				ID:             row.ID,
				Name:           row.Name,
				DescriptionPOI: row.Description,
				Category:       row.Category,
				Address:        row.Address,
				Latitude:       row.Latitude,
				Longitude:      row.Longitude,
				CityID:         cityID,
			},
			TextRank:       row.TextRank,
			NameSimilarity: row.NameSimilarity,
			ExactName:      row.ExactName,
		}
	}

	l.InfoContext(ctx, "Lexical POI search completed",
		slog.Int("count", len(hits)), slog.String("city_id", cityID.String()))
	span.SetAttributes(attribute.Int("results.count", len(hits)))
	span.SetStatus(codes.Ok, "Lexical search completed")

	return hits, nil
}
