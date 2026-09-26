// Package gastronomy serves GastronomyService: a city's typical gastronomy,
// looked up on its own. The generation itself belongs to the chat service,
// which produces the same section on itinerary and discovery answers; this
// package is only the RPC surface over it.
package gastronomy

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/presenter"
	chatservice "github.com/FACorreiaa/loci-connect-api/internal/domain/chat/service"
	"github.com/FACorreiaa/loci-connect-api/internal/types"

	gastronomyv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/gastronomy"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/gastronomy/gastronomyconnect"
)

// Generator produces a city's gastronomy. chatservice.ServiceImpl satisfies
// it. cached reports whether the answer was served from cache.
type Generator interface {
	GetCityGastronomy(ctx context.Context, cityID uuid.UUID, cityName string) (g *locitypes.CityGastronomy, cached bool, err error)
}

// Handler implements the GastronomyService RPCs.
type Handler struct {
	gastronomyconnect.UnimplementedGastronomyServiceHandler
	gen    Generator
	logger *slog.Logger
}

// NewHandler wires a gastronomy handler.
func NewHandler(gen Generator, logger *slog.Logger) *Handler {
	return &Handler{gen: gen, logger: logger}
}

// GetCityGastronomy returns the typical gastronomy of one city.
func (h *Handler) GetCityGastronomy(
	ctx context.Context,
	req *connect.Request[gastronomyv1.GetCityGastronomyRequest],
) (*connect.Response[gastronomyv1.GetCityGastronomyResponse], error) {
	var cityID uuid.UUID
	if raw := req.Msg.GetCityId(); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("city_id must be a UUID"))
		}
		cityID = id
	}

	g, cached, err := h.gen.GetCityGastronomy(ctx, cityID, req.Msg.GetCityName())
	switch {
	case errors.Is(err, chatservice.ErrGastronomyCityNotFound):
		return nil, connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, chatservice.ErrGastronomyUnavailable):
		return nil, connect.NewError(connect.CodeNotFound, err)
	case err != nil:
		h.logger.ErrorContext(ctx, "get city gastronomy failed",
			slog.String("city_name", req.Msg.GetCityName()), slog.Any("error", err))
		return nil, connect.NewError(connect.CodeUnavailable,
			errors.New("could not load this city's gastronomy right now, please try again"))
	}

	return connect.NewResponse(&gastronomyv1.GetCityGastronomyResponse{
		Gastronomy: presenter.ToCityGastronomy(g),
		Cached:     cached,
	}), nil
}
