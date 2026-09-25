package handler

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	poiv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/poi"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/poi/poiconnect"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/poi"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/poi/presenter"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/apierr"
)

type POIHandler struct {
	poiconnect.UnimplementedPOIServiceHandler
	service poi.Service
}

func NewPOIHandler(svc poi.Service) *POIHandler {
	return &POIHandler{service: svc}
}

// searchMode is how SearchPOI answers a request.
type searchMode int

const (
	// searchSemanticCity is a semantic search inside one named city (with the
	// LLM fallback when the city has nothing).
	searchSemanticCity searchMode = iota
	// searchHybrid ranks by distance from the given point and by meaning.
	searchHybrid
	// searchSemanticAll is a semantic search across every city.
	searchSemanticAll
)

// defaultHybridRadiusKm applies when a location-based search sends no radius.
// Zero would match nothing (ST_DWithin with a 0 m radius).
const defaultHybridRadiusKm = 25.0

// nearbyCityPlaceholder is what clients send as city_name for "around me";
// it is not a city and must never be looked up (or handed to the LLM) as one.
const nearbyCityPlaceholder = "nearby"

// resolveSearch picks the search to run. city_name is optional: empty (or the
// "nearby" placeholder) searches around latitude/longitude when a location is
// set, otherwise across every city. It returns the cleaned city name.
func resolveSearch(searchType, cityName string, lat, lon float64) (searchMode, string) {
	city := strings.TrimSpace(cityName)
	if strings.EqualFold(city, nearbyCityPlaceholder) {
		city = ""
	}
	hasLocation := lat != 0 || lon != 0

	if searchType == "hybrid" && hasLocation {
		return searchHybrid, city
	}
	switch {
	case city != "":
		return searchSemanticCity, city
	case hasLocation:
		return searchHybrid, ""
	default:
		return searchSemanticAll, ""
	}
}

func (h *POIHandler) SearchPOI(ctx context.Context, req *connect.Request[poiv1.SearchPOIRequest]) (*connect.Response[poiv1.SearchPOIResponse], error) {
	searchType := ""
	if req.Msg.SearchType != nil {
		searchType = *req.Msg.SearchType
	}
	query := req.Msg.Query
	mode, cityName := resolveSearch(searchType, req.Msg.CityName, req.Msg.Latitude, req.Msg.Longitude)

	var pois []locitypes.POIDetailedInfo
	var err error

	switch mode {
	case searchHybrid:
		filter := locitypes.POIFilter{Radius: defaultHybridRadiusKm}
		filter.Location.Latitude = req.Msg.Latitude
		filter.Location.Longitude = req.Msg.Longitude
		if req.Msg.RadiusKm != nil {
			filter.Radius = *req.Msg.RadiusKm
		}
		pois, err = h.service.SearchPOIsHybrid(ctx, filter, query, 0.5) // Default weight
	case searchSemanticCity:
		pois, err = h.service.SearchPOIsByQueryAndCity(ctx, query, cityName)
	default:
		pois, err = h.service.SearchPOIsSemantic(ctx, query, 20)
	}

	if err != nil {
		return nil, apierr.ToConnect(err)
	}

	return connect.NewResponse(&poiv1.SearchPOIResponse{
		Pois: presenter.ToPOIProtos(pois),
	}), nil
}

func (h *POIHandler) GetPOI(ctx context.Context, req *connect.Request[poiv1.GetPOIRequest]) (*connect.Response[poiv1.GetPOIResponse], error) {
	poiID, err := uuid.Parse(req.Msg.PoiId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid POI ID"))
	}

	poi, err := h.service.GetPOI(ctx, poiID)
	if err != nil {
		return nil, apierr.ToConnect(err)
	}
	if poi == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("POI not found"))
	}

	return connect.NewResponse(&poiv1.GetPOIResponse{
		Poi: presenter.ToPOIProto(poi),
	}), nil
}
