package handler

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	cityv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/city"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/city/cityconnect"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/city"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/city/presenter"
)

// defaultSearchLimit caps an unbounded SearchCities call; maxSearchLimit stops a
// client from asking for the whole table.
const (
	defaultSearchLimit = 20
	maxSearchLimit     = 100
)

type CityHandler struct {
	cityconnect.UnimplementedCityServiceHandler
	service city.Service
}

func NewCityHandler(svc city.Service) *CityHandler {
	return &CityHandler{service: svc}
}

// GetCity resolves a city by id or by name, whichever the request's oneof carries.
func (h *CityHandler) GetCity(
	ctx context.Context,
	req *connect.Request[cityv1.GetCityRequest],
) (*connect.Response[cityv1.GetCityResponse], error) {
	switch {
	case req.Msg.GetCityId() != "":
		id, err := uuid.Parse(req.Msg.GetCityId())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				errors.New("city_id must be a UUID"))
		}
		found, err := h.service.GetCityByID(ctx, id)
		if err != nil {
			return nil, mapCityError(err)
		}
		return connect.NewResponse(&cityv1.GetCityResponse{
			City: presenter.ToCityProto(*found),
		}), nil

	case req.Msg.GetCityName() != "":
		found, err := h.service.GetCityByName(ctx, req.Msg.GetCityName())
		if err != nil {
			return nil, mapCityError(err)
		}
		return connect.NewResponse(&cityv1.GetCityResponse{
			City: presenter.ToCityProto(*found),
		}), nil
	}

	return nil, connect.NewError(connect.CodeInvalidArgument,
		errors.New("one of city_id or city_name is required"))
}

// SearchCities backs the client's city picker. An empty query is valid and
// returns the first alphabetical page - that is the "browse" case the client
// sends on mount.
func (h *CityHandler) SearchCities(
	ctx context.Context,
	req *connect.Request[cityv1.SearchCitiesRequest],
) (*connect.Response[cityv1.SearchCitiesResponse], error) {
	limit := defaultSearchLimit
	if req.Msg.Limit != nil && *req.Msg.Limit > 0 {
		limit = int(*req.Msg.Limit)
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	cities, err := h.service.SearchCities(ctx, req.Msg.GetQuery(), limit)
	if err != nil {
		return nil, mapCityError(err)
	}

	return connect.NewResponse(&cityv1.SearchCitiesResponse{
		Cities: presenter.ToCityProtos(cities),
	}), nil
}

func mapCityError(err error) error {
	if errors.Is(err, city.ErrCityNotFound) {
		return connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewError(connect.CodeInternal, err)
}
