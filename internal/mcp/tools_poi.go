package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	recommendationv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recommendation"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/retrieval"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Result bounds come from the shared retrieval package rather than from local
// literals, so "how many results" and "how long a description" mean the same
// thing on the MCP surface, the Connect handlers and the chat path.
const (
	maxToolResults   = retrieval.MaxEvidence
	descriptionLimit = retrieval.MaxDescriptionChars
)

// POISummary is the compact POI representation returned by list tools.
type POISummary struct {
	ID                  string               `json:"id,omitempty"`
	Name                string               `json:"name"`
	Category            string               `json:"category,omitempty"`
	Description         string               `json:"description,omitempty"`
	Latitude            float64              `json:"latitude,omitempty"`
	Longitude           float64              `json:"longitude,omitempty"`
	Address             string               `json:"address,omitempty"`
	Rating              float64              `json:"rating,omitempty"`
	PriceRange          string               `json:"price_range,omitempty"`
	City                string               `json:"city,omitempty"`
	RecommendationTrace *RecommendationTrace `json:"recommendation_trace,omitempty"`

	// DistanceKm is populated only by the tools that actually measure a
	// distance. This field used to be `distance_meters` filled from
	// POIDetailedInfo.Distance, which holds kilometres on the spatial code paths
	// and a raw cosine similarity score on the vector ones — so an agent reading
	// "distance_meters: 0.82" was being told a similarity score was a distance.
	DistanceKm float64 `json:"distance_km,omitempty"`

	// Source is where the underlying data came from (for example "llm").
	// MatchReason is why this result surfaced: lexical, semantic, both, nearby.
	// Together they let a calling agent cite Loci the way Loci cites itself.
	Source      string `json:"source,omitempty"`
	MatchReason string `json:"match_reason,omitempty"`
}

// RecommendationTrace is returned by recommendation tools and accepted by outcome tools.
type RecommendationTrace struct {
	RunID             string `json:"run_id" jsonschema:"opaque recommendation run id"`
	ItemID            string `json:"item_id" jsonschema:"recommended item id"`
	Rank              int32  `json:"rank" jsonschema:"zero-based result rank"`
	AlgorithmVersion  string `json:"algorithm_version" jsonschema:"ranking algorithm version"`
	ExperimentVariant string `json:"experiment_variant" jsonschema:"experiment cohort"`
	Surface           string `json:"surface" jsonschema:"discover or nearby"`
}

type poiListOutput struct {
	Results []POISummary `json:"results"`
	Count   int          `json:"count"`
	// Truncated is set when more results existed than the tool returns.
	Truncated bool `json:"truncated,omitempty"`
}

func callerUserID(ctx context.Context) (uuid.UUID, error) {
	idStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok {
		return uuid.Nil, errors.New("unauthenticated")
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return uuid.Nil, errors.New("invalid user identity")
	}
	return id, nil
}

// summarize compacts POIs for a tool response.
//
// distances is optional and keyed by POI id; only tools that genuinely measured
// a distance supply it. Callers that did not measure pass nil, and the field is
// simply omitted rather than filled with whatever POIDetailedInfo.Distance
// happens to be carrying on that code path.
func summarize(pois []locitypes.POIDetailedInfo, distances map[uuid.UUID]float64) poiListOutput {
	out := poiListOutput{Count: len(pois)}
	if len(pois) > maxToolResults {
		pois = pois[:maxToolResults]
		out.Truncated = true
	}
	for _, p := range pois {
		desc := p.DescriptionPOI
		if desc == "" {
			desc = p.Description
		}
		// Rune-aware: a byte slice here splits multi-byte characters and emits
		// invalid UTF-8 for names like "Café" or "Belém".
		desc = retrieval.TruncateRunes(desc, descriptionLimit)

		var id string
		if p.ID != uuid.Nil {
			id = p.ID.String()
		}
		out.Results = append(out.Results, POISummary{
			ID:          id,
			Name:        p.Name,
			Category:    p.Category,
			Description: desc,
			Latitude:    p.Latitude,
			Longitude:   p.Longitude,
			Address:     p.Address,
			Rating:      p.Rating,
			PriceRange:  p.PriceRange,
			DistanceKm:  distances[p.ID],
			City:        p.City,
			Source:      p.Source,
		})
	}
	return out
}

func summarizeRecommendations(ctx context.Context, deps Deps, pois []locitypes.POIDetailedInfo, surface recommendationv1.RecommendationSurface, distances map[uuid.UUID]float64) poiListOutput {
	out := summarize(pois, distances)
	userID, err := callerUserID(ctx)
	if err != nil {
		return out
	}
	runID := uuid.NewString()
	variant := preference.ExperimentVariant(userID)
	events := make([]*recommendationv1.RecommendationEvent, 0, len(out.Results))
	for index := range out.Results {
		trace := &RecommendationTrace{
			RunID: runID, ItemID: out.Results[index].ID, Rank: int32(index),
			AlgorithmVersion: "poi-hybrid-v1", ExperimentVariant: variant,
			Surface: strings.ToLower(strings.TrimPrefix(surface.String(), "RECOMMENDATION_SURFACE_")),
		}
		out.Results[index].RecommendationTrace = trace
		events = append(events, &recommendationv1.RecommendationEvent{
			ClientEventId: uuid.NewString(),
			EventType:     recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_DELIVERED,
			Trace:         traceToProto(trace, surface),
			OccurredAt:    timestamppb.New(time.Now()),
			PoiId:         stringPointer(out.Results[index].ID),
		})
	}
	if deps.Recommendation != nil && len(events) > 0 {
		traces := make([]*recommendationv1.RecommendationTrace, 0, len(events))
		for _, event := range events {
			traces = append(traces, event.GetTrace())
		}
		if err := deps.Recommendation.IssueTraces(ctx, userID, traces); err != nil {
			if deps.Logger != nil {
				deps.Logger.ErrorContext(ctx, "failed to issue MCP recommendation attribution", slog.Any("error", err))
			}
			for index := range out.Results {
				out.Results[index].RecommendationTrace = nil
			}
			return out
		}
		_, _ = deps.Recommendation.RecordEvents(ctx, connect.NewRequest(&recommendationv1.RecordEventsRequest{Events: events}))
	}
	return out
}

func traceToProto(trace *RecommendationTrace, surface recommendationv1.RecommendationSurface) *recommendationv1.RecommendationTrace {
	if trace == nil {
		return nil
	}
	return &recommendationv1.RecommendationTrace{
		RunId: trace.RunID, ItemId: trace.ItemID, Rank: trace.Rank,
		AlgorithmVersion: trace.AlgorithmVersion, ExperimentVariant: trace.ExperimentVariant,
		Surface: surface, Channel: recommendationv1.RecommendationChannel_RECOMMENDATION_CHANNEL_MCP,
	}
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func recordMCPOutcome(ctx context.Context, deps Deps, trace *RecommendationTrace, poiID string, eventType recommendationv1.RecommendationEventType) {
	if deps.Recommendation == nil || trace == nil {
		return
	}
	surface := recommendationv1.RecommendationSurface_RECOMMENDATION_SURFACE_DISCOVER
	if trace.Surface == "nearby" {
		surface = recommendationv1.RecommendationSurface_RECOMMENDATION_SURFACE_NEARBY
	}
	_, _ = deps.Recommendation.RecordEvents(ctx, connect.NewRequest(&recommendationv1.RecordEventsRequest{
		Events: []*recommendationv1.RecommendationEvent{{
			ClientEventId: uuid.NewString(), EventType: eventType, Trace: traceToProto(trace, surface),
			OccurredAt: timestamppb.Now(), PoiId: stringPointer(poiID),
		}},
	}))
}

type searchPOIsInput struct {
	Query     string  `json:"query,omitempty" jsonschema:"free-text search, e.g. 'romantic rooftop bars'; empty lists POIs near the location"`
	Latitude  float64 `json:"latitude" jsonschema:"search center latitude"`
	Longitude float64 `json:"longitude" jsonschema:"search center longitude"`
	RadiusKm  float64 `json:"radius_km,omitempty" jsonschema:"search radius in kilometers, default 5"`
	Category  string  `json:"category,omitempty" jsonschema:"optional category filter, e.g. restaurant, museum, park"`
}

type getPOIDetailsInput struct {
	ID string `json:"id" jsonschema:"POI id returned by search_pois or find_nearby"`
}

type findNearbyInput struct {
	Latitude  float64 `json:"latitude" jsonschema:"center latitude"`
	Longitude float64 `json:"longitude" jsonschema:"center longitude"`
	RadiusKm  float64 `json:"radius_km,omitempty" jsonschema:"radius in kilometers, default 2"`
	Category  string  `json:"category" jsonschema:"one of: restaurant, hotel, activity, attraction, any"`
	// Category-specific refinements, all optional.
	CuisineType  string `json:"cuisine_type,omitempty" jsonschema:"restaurants only, e.g. sushi, italian"`
	PriceRange   string `json:"price_range,omitempty" jsonschema:"restaurants only, e.g. $, $$, $$$"`
	ActivityType string `json:"activity_type,omitempty" jsonschema:"activities only, e.g. hiking, museum"`
	StarRating   string `json:"star_rating,omitempty" jsonschema:"hotels only, e.g. 4"`
}

func registerPOITools(server *mcp.Server, deps Deps) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_pois",
		Description: "Search Loci's points-of-interest database near a location. Combines keyword and semantic matching when a query is given.",
	}, guardTool("search_pois", func(ctx context.Context, _ *mcp.CallToolRequest, in searchPOIsInput) (*mcp.CallToolResult, poiListOutput, error) {
		radius := in.RadiusKm
		if radius <= 0 {
			radius = 5
		}
		filter := locitypes.POIFilter{
			Location: locitypes.GeoPoint{Latitude: in.Latitude, Longitude: in.Longitude},
			Radius:   radius,
			Category: in.Category,
		}

		var (
			pois []locitypes.POIDetailedInfo
			err  error
		)
		if in.Query != "" {
			pois, err = deps.POIService.SearchPOIsHybrid(ctx, filter, in.Query, 0.6)
		} else {
			pois, err = deps.POIService.SearchPOIs(ctx, filter)
		}
		if err != nil {
			return nil, poiListOutput{}, toolError(err)
		}
		// This tool is spatial — it takes a centre and a radius — so the
		// distances it reports are real kilometres from that centre.
		out := summarizeRecommendations(ctx, deps, pois,
			recommendationv1.RecommendationSurface_RECOMMENDATION_SURFACE_DISCOVER,
			measuredDistances(pois))
		reason := string(retrieval.MatchNearby)
		if in.Query != "" {
			reason = string(retrieval.MatchBoth)
		}
		labelMatchReason(&out, reason)
		return nil, out, nil
	}))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_poi_details",
		Description: "Fetch full details for a single point of interest by its Loci id.",
	}, guardTool("get_poi_details", func(ctx context.Context, _ *mcp.CallToolRequest, in getPOIDetailsInput) (*mcp.CallToolResult, *locitypes.POIDetailedInfo, error) {
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid poi id %q", in.ID)
		}
		poiInfo, err := deps.POIService.GetPOI(ctx, id)
		if err != nil {
			return nil, nil, toolError(err)
		}
		return nil, poiInfo, nil
	}))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "find_nearby",
		Description: "Find restaurants, hotels, activities, or attractions near a location. Results come from Loci's database and are AI-enriched on first request for an area.",
	}, guardTool("find_nearby", func(ctx context.Context, _ *mcp.CallToolRequest, in findNearbyInput) (*mcp.CallToolResult, poiListOutput, error) {
		userID, err := callerUserID(ctx)
		if err != nil {
			return nil, poiListOutput{}, err
		}
		radiusM := in.RadiusKm * 1000
		if radiusM <= 0 {
			radiusM = 2000
		}

		var pois []locitypes.POIDetailedInfo
		switch in.Category {
		case "restaurant":
			pois, err = deps.POIService.GetNearbyRestaurants(ctx, userID, in.Latitude, in.Longitude, radiusM, in.CuisineType, in.PriceRange)
		case "hotel":
			pois, err = deps.POIService.GetNearbyHotels(ctx, userID, in.Latitude, in.Longitude, radiusM, in.StarRating, "")
		case "activity":
			pois, err = deps.POIService.GetNearbyActivities(ctx, userID, in.Latitude, in.Longitude, radiusM, in.ActivityType, "")
		case "attraction":
			pois, err = deps.POIService.GetNearbyAttractions(ctx, userID, in.Latitude, in.Longitude, radiusM, "", "")
		case "any", "":
			pois, err = deps.POIService.GetGeneralPOIByDistance(ctx, userID, in.Latitude, in.Longitude, radiusM)
		default:
			return nil, poiListOutput{}, fmt.Errorf("unknown category %q: use restaurant, hotel, activity, attraction, or any", in.Category)
		}
		if err != nil {
			return nil, poiListOutput{}, toolError(err)
		}
		out := summarizeRecommendations(ctx, deps, pois,
			recommendationv1.RecommendationSurface_RECOMMENDATION_SURFACE_NEARBY,
			measuredDistances(pois))
		labelMatchReason(&out, string(retrieval.MatchNearby))
		return nil, out, nil
	}))
}

// measuredDistances extracts distances for results produced by a spatial query.
//
// Only call this from a tool whose query was actually a radius search: on those
// paths POIDetailedInfo.Distance holds kilometres. The vector search paths reuse
// the same field for a cosine similarity score, and reporting that as a distance
// is what this indirection exists to prevent.
func measuredDistances(pois []locitypes.POIDetailedInfo) map[uuid.UUID]float64 {
	out := make(map[uuid.UUID]float64, len(pois))
	for _, p := range pois {
		if p.ID != uuid.Nil && p.Distance > 0 {
			out[p.ID] = p.Distance
		}
	}
	return out
}

// labelMatchReason stamps why these results surfaced, so a calling agent can
// tell a keyword hit from a semantic one.
func labelMatchReason(out *poiListOutput, reason string) {
	for i := range out.Results {
		out.Results[i].MatchReason = reason
	}
}
