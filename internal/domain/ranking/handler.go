package ranking

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	rankingv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/ranking"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/ranking/rankingconnect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Handler implements RankingService.
type Handler struct {
	rankingconnect.UnimplementedRankingServiceHandler
	svc *Service
	log *slog.Logger
}

// NewHandler builds the ranking Connect handler.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log.With(slog.String("component", "ranking-handler"))}
}

// userID and toConnectErr are local, as in the other domains (see
// travelhistory/handler.go for why).
func userID(ctx context.Context) (uuid.UUID, error) {
	s, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || s == "" {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}
	return id, nil
}

func (h *Handler) toConnectErr(ctx context.Context, op string, err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("you haven't ranked this place"))
	case errors.Is(err, ErrNotPending):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("this place is already ranked"))
	case errors.Is(err, ErrNotFriends):
		return connect.NewError(connect.CodePermissionDenied, ErrNotFriends)
	}
	h.log.ErrorContext(ctx, op+" failed", slog.Any("error", err))
	return connect.NewError(connect.CodeInternal, errors.New("rankings unavailable"))
}

func (h *Handler) RatePlace(ctx context.Context, req *connect.Request[rankingv1.RatePlaceRequest]) (*connect.Response[rankingv1.RatePlaceResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	m := req.Msg
	place, next, err := h.svc.RatePlace(ctx, uid, RateInput{
		POIID:    m.GetPoiId(),
		POIName:  m.GetPoiName(),
		Category: m.GetCategory(),
		CityName: m.GetCityName(),
		Verdict:  Verdict(m.GetVerdict()),
	})
	if err != nil {
		return nil, h.toConnectErr(ctx, "rate place", err)
	}
	return connect.NewResponse(&rankingv1.RatePlaceResponse{Place: toProto(place), Next: comparisonToProto(next)}), nil
}

func (h *Handler) ComparePlaces(ctx context.Context, req *connect.Request[rankingv1.ComparePlacesRequest]) (*connect.Response[rankingv1.ComparePlacesResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	place, next, err := h.svc.ComparePlaces(ctx, uid, req.Msg.GetPoiId(), req.Msg.GetOtherPoiId(), req.Msg.GetBetter())
	if err != nil {
		return nil, h.toConnectErr(ctx, "compare places", err)
	}
	return connect.NewResponse(&rankingv1.ComparePlacesResponse{Place: toProto(place), Next: comparisonToProto(next)}), nil
}

func (h *Handler) DeleteRanking(ctx context.Context, req *connect.Request[rankingv1.DeleteRankingRequest]) (*connect.Response[rankingv1.DeleteRankingResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	deleted, err := h.svc.Delete(ctx, uid, req.Msg.GetPoiId())
	if err != nil {
		return nil, h.toConnectErr(ctx, "delete ranking", err)
	}
	return connect.NewResponse(&rankingv1.DeleteRankingResponse{Deleted: deleted}), nil
}

func (h *Handler) ListMyRankings(ctx context.Context, req *connect.Request[rankingv1.ListMyRankingsRequest]) (*connect.Response[rankingv1.ListRankingsResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	places, err := h.svc.ListMine(ctx, uid, req.Msg.GetCityName(), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, h.toConnectErr(ctx, "list my rankings", err)
	}
	return connect.NewResponse(&rankingv1.ListRankingsResponse{Places: listToProto(places)}), nil
}

func (h *Handler) ListUserRankings(ctx context.Context, req *connect.Request[rankingv1.ListUserRankingsRequest]) (*connect.Response[rankingv1.ListRankingsResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	owner, err := uuid.Parse(req.Msg.GetUserId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}
	places, err := h.svc.ListFor(ctx, uid, owner, req.Msg.GetCityName(), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, h.toConnectErr(ctx, "list user rankings", err)
	}
	return connect.NewResponse(&rankingv1.ListRankingsResponse{Places: listToProto(places)}), nil
}

func toProto(p Place) *rankingv1.RankedPlace {
	out := &rankingv1.RankedPlace{
		PoiId:    p.POIID,
		Name:     p.Name,
		Category: p.Category,
		CityName: p.CityName,
		Verdict:  rankingv1.Verdict(p.Verdict),
		Rank:     int32(p.Rank),
		Score:    p.Score,
		Pending:  p.Pending,
	}
	if !p.UpdatedAt.IsZero() {
		out.UpdatedAt = timestamppb.New(p.UpdatedAt)
	}
	return out
}

func listToProto(places []Place) []*rankingv1.RankedPlace {
	out := make([]*rankingv1.RankedPlace, len(places))
	for i, p := range places {
		out[i] = toProto(p)
	}
	return out
}

func comparisonToProto(c *Comparison) *rankingv1.Comparison {
	if c == nil {
		return nil
	}
	return &rankingv1.Comparison{PoiId: c.POIID, Name: c.Name}
}
