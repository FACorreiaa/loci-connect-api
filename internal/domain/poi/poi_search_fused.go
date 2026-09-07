package poi

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/retrieval"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// SearchResult is a POI plus why it surfaced.
type SearchResult struct {
	POI    locitypes.POIDetailedInfo
	Reason retrieval.MatchReason
}

// SearchPOIsFused answers a text query with two independent lanes and fuses
// them, which is what "hybrid search" was always supposed to mean.
//
// Before this, search_pois called SearchPOIsHybrid alone. That has no lexical
// arm at all — the query string never reaches SQL, only its embedding — and its
// ranking degrades to `1/(1+distance)` whenever the row has no embedding. No POI
// in the general corpus had one, because the backfill had no caller. So every
// query returned the same POIs nearest the given point, in the same order:
// "Cabo Girão" and "levada" and an empty string were indistinguishable.
//
// The lanes:
//
//   - lexical, via SearchPOIsLexical — full-text over search_tsv plus a trigram
//     arm for typos. Deterministic, needs no embedding, and is the only lane
//     that can match a proper noun. It already existed and was wired only into
//     chat grounding.
//   - semantic/spatial, via SearchPOIsHybrid — the previous behaviour, kept
//     because it finds things a keyword cannot ("somewhere quiet for coffee").
//
// Fusion is retrieval.FuseRRF, the same function chat grounding uses, so a
// result's rank means the same thing on both surfaces. A lane that fails is
// logged and skipped rather than failing the search: one working lane still
// answers the question. Both failing is an error.
//
// The returned reason is per POI and reflects the lane that actually matched,
// which is what makes `match_reason` on the MCP surface true rather than
// decorative.
func (s *ServiceImpl) SearchPOIsFused(
	ctx context.Context,
	filter locitypes.POIFilter,
	query string,
	semanticWeight float64,
) ([]SearchResult, error) {
	ctx, span := otel.Tracer("POIService").Start(ctx, "SearchPOIsFused", trace.WithAttributes(
		attribute.Int("query.length", len(query)),
		attribute.Float64("semantic.weight", semanticWeight),
		attribute.Float64("radius.km", filter.Radius),
	))
	defer span.End()

	l := s.logger.With(slog.String("method", "SearchPOIsFused"))

	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil, fmt.Errorf("query must not be empty; use SearchPOIs for an unqueried listing")
	}
	// Enforced here rather than only advertised. retrieval.MaxQueryChars is what
	// the status tool reports as the limit, and nothing was checking it.
	if len(trimmed) > retrieval.MaxQueryChars {
		return nil, fmt.Errorf("query must be at most %d characters; got %d",
			retrieval.MaxQueryChars, len(trimmed))
	}

	byID := make(map[uuid.UUID]locitypes.POIDetailedInfo)
	var lanes []retrieval.Ranked

	// Lexical. cityID is uuid.Nil so the search is not confined to one city —
	// search_pois is given a point and a radius, not a city — and the radius
	// bound is applied below, after both lanes have had their say.
	lexical, lexErr := s.poiRepository.SearchPOIsLexical(ctx, uuid.Nil, trimmed, retrieval.MaxSearchResults)
	if lexErr != nil {
		l.WarnContext(ctx, "lexical lane failed", slog.Any("error", lexErr))
		span.AddEvent("lexical_lane_failed")
	} else if len(lexical) > 0 {
		ids := make([]uuid.UUID, 0, len(lexical))
		for _, hit := range lexical {
			byID[hit.POI.ID] = hit.POI
			ids = append(ids, hit.POI.ID)
		}
		lanes = append(lanes, retrieval.Ranked{Reason: retrieval.MatchLexical, IDs: ids})
	}

	// Semantic + spatial, the previous behaviour.
	semantic, semErr := s.SearchPOIsHybrid(ctx, filter, trimmed, semanticWeight)
	if semErr != nil {
		l.WarnContext(ctx, "semantic lane failed", slog.Any("error", semErr))
		span.AddEvent("semantic_lane_failed")
	} else if len(semantic) > 0 {
		ids := make([]uuid.UUID, 0, len(semantic))
		for _, poi := range semantic {
			if _, known := byID[poi.ID]; !known {
				byID[poi.ID] = poi
			}
			ids = append(ids, poi.ID)
		}
		lanes = append(lanes, retrieval.Ranked{Reason: retrieval.MatchSemantic, IDs: ids})
	}

	if len(lanes) == 0 {
		if lexErr != nil && semErr != nil {
			err := fmt.Errorf("both search lanes failed: lexical: %w; semantic: %v", lexErr, semErr)
			span.RecordError(err)
			span.SetStatus(codes.Error, "both lanes failed")
			return nil, err
		}
		// Both lanes worked and neither matched anything. That is an answer.
		return nil, nil
	}

	fused := retrieval.FuseRRF(lanes...)
	out := make([]SearchResult, 0, len(fused))
	for _, f := range fused {
		poi, ok := byID[f.POIID]
		if !ok {
			continue
		}
		// The radius the caller asked for is a hard bound on both lanes: a
		// lexical hit on the far side of the country is not a result for a
		// search centred here. Distances are recomputed from the search centre
		// rather than trusted, because the lexical lane does not measure one.
		if filter.Radius > 0 {
			km := calculateDistance(
				filter.Location.Latitude, filter.Location.Longitude,
				poi.Latitude, poi.Longitude,
			)
			if km > filter.Radius {
				continue
			}
			poi.Distance = km
		}
		out = append(out, SearchResult{POI: poi, Reason: f.Reason})
	}

	l.InfoContext(ctx, "fused search completed",
		slog.Int("lanes", len(lanes)),
		slog.Int("lexical", len(lexical)),
		slog.Int("semantic", len(semantic)),
		slog.Int("results", len(out)))
	span.SetAttributes(attribute.Int("results", len(out)))
	span.SetStatus(codes.Ok, "fused search completed")

	return out, nil
}
