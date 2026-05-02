package handler

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	poiv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/poi"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/poi/poiconnect"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/poi"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/poi/presenter"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

type POIHandler struct {
	poiconnect.UnimplementedPOIServiceHandler
	service poi.Service
}

func NewPOIHandler(svc poi.Service) *POIHandler {
	return &POIHandler{service: svc}
}

func (h *POIHandler) SearchPOI(ctx context.Context, req *connect.Request[poiv1.SearchPOIRequest]) (*connect.Response[poiv1.SearchPOIResponse], error) {
	// Map request to filter
	filter := locitypes.POIFilter{
		// Mapping depends on SearchPOIRequest fields availability
		// Assuming minimal mapping for now based on what I saw in cat output (query, city_name, lat, lon)
		// and using SearchPOIsSemantic or SearchPOIsByQueryAndCity logic from service
	}
	// Actually, the service has SearchPOIs(filter) OR SearchPOIsSemantic
	// The proto request has `query`, `city_name`, `latitude`, `longitude`, `search_type`

	searchType := ""
	if req.Msg.SearchType != nil {
		searchType = *req.Msg.SearchType
	}
	query := req.Msg.Query
	cityName := req.Msg.CityName

	var pois []locitypes.POIDetailedInfo
	var err error

	switch searchType {
	case "semantic":
		if cityName != "" {
			// Need city UUID if using SearchPOIsSemanticByCity...
			// Service method SearchPOIsSemanticByCity requires UUID.
			// Helper SearchPOIsByQueryAndCity takes string name.
			pois, err = h.service.SearchPOIsByQueryAndCity(ctx, query, cityName)
		} else {
			limit := 20 // Default
			pois, err = h.service.SearchPOIsSemantic(ctx, query, limit)
		}
	case "hybrid":
		// Needs filter + query
		filter.Location.Latitude = req.Msg.Latitude
		filter.Location.Longitude = req.Msg.Longitude
		if req.Msg.RadiusKm != nil {
			filter.Radius = *req.Msg.RadiusKm
		}
		pois, err = h.service.SearchPOIsHybrid(ctx, filter, query, 0.5) // Default weight
	default:
		// Default to text search or semantic?
		// Using SearchPOIsByQueryAndCity as generic entry point if query/city provided
		pois, err = h.service.SearchPOIsByQueryAndCity(ctx, query, cityName)
	}

	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&poiv1.SearchPOIResponse{
		Pois: presenter.ToPOIProtos(pois),
		// Pagination metadata if needed
	}), nil
}

func (h *POIHandler) GetPOI(ctx context.Context, req *connect.Request[poiv1.GetPOIRequest]) (*connect.Response[poiv1.GetPOIResponse], error) {
	poiID, err := uuid.Parse(req.Msg.PoiId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid POI ID"))
	}

	poi, err := h.service.GetPOI(ctx, poiID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if poi == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("POI not found"))
	}

	return connect.NewResponse(&poiv1.GetPOIResponse{
		Poi: presenter.ToPOIProto(poi),
	}), nil
}
