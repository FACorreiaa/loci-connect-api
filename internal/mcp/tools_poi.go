package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// maxToolResults caps list responses so a broad search does not flood the
// client's context window.
const maxToolResults = 20

// descriptionLimit truncates long AI-generated descriptions in list results.
const descriptionLimit = 300

// POISummary is the compact POI representation returned by list tools.
type POISummary struct {
	ID          string  `json:"id,omitempty"`
	Name        string  `json:"name"`
	Category    string  `json:"category,omitempty"`
	Description string  `json:"description,omitempty"`
	Latitude    float64 `json:"latitude,omitempty"`
	Longitude   float64 `json:"longitude,omitempty"`
	Address     string  `json:"address,omitempty"`
	Rating      float64 `json:"rating,omitempty"`
	PriceRange  string  `json:"price_range,omitempty"`
	DistanceM   float64 `json:"distance_meters,omitempty"`
	City        string  `json:"city,omitempty"`
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

func summarize(pois []locitypes.POIDetailedInfo) poiListOutput {
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
		if len(desc) > descriptionLimit {
			desc = desc[:descriptionLimit] + "…"
		}
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
			DistanceM:   p.Distance,
			City:        p.City,
		})
	}
	return out
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
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchPOIsInput) (*mcp.CallToolResult, poiListOutput, error) {
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
		return nil, summarize(pois), nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_poi_details",
		Description: "Fetch full details for a single point of interest by its Loci id.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getPOIDetailsInput) (*mcp.CallToolResult, *locitypes.POIDetailedInfo, error) {
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid poi id %q", in.ID)
		}
		poiInfo, err := deps.POIService.GetPOI(ctx, id)
		if err != nil {
			return nil, nil, toolError(err)
		}
		return nil, poiInfo, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "find_nearby",
		Description: "Find restaurants, hotels, activities, or attractions near a location. Results come from Loci's database and are AI-enriched on first request for an area.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in findNearbyInput) (*mcp.CallToolResult, poiListOutput, error) {
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
		return nil, summarize(pois), nil
	})
}
