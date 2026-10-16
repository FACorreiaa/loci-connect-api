package recap

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	recapv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recap"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recap/recapconnect"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/recap/render"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Handler implements RecapService.
type Handler struct {
	recapconnect.UnimplementedRecapServiceHandler
	svc *Service
	log *slog.Logger
}

// NewHandler builds the recap Connect handler.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log.With(slog.String("component", "recap-handler"))}
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
		return connect.NewError(connect.CodeNotFound, errors.New("recap not found"))
	case errors.Is(err, ErrNotReady):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("recap is not ready"))
	case errors.Is(err, render.ErrUnsupportedCard):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("this card has no image"))
	}
	h.log.ErrorContext(ctx, op+" failed", slog.Any("error", err))
	return connect.NewError(connect.CodeInternal, errors.New("recap unavailable"))
}

func (h *Handler) GetTripRecap(ctx context.Context, req *connect.Request[recapv1.GetTripRecapRequest]) (*connect.Response[recapv1.GetRecapResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	tripID, err := uuid.Parse(req.Msg.GetTripId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid trip ID"))
	}
	rec, err := h.svc.TripRecap(ctx, uid, tripID, req.Msg.GetRefresh())
	if err != nil {
		return nil, h.toConnectErr(ctx, "get trip recap", err)
	}
	return connect.NewResponse(&recapv1.GetRecapResponse{Recap: rec}), nil
}

func (h *Handler) GetYearInReview(ctx context.Context, req *connect.Request[recapv1.GetYearInReviewRequest]) (*connect.Response[recapv1.GetRecapResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	rec, err := h.svc.YearInReview(ctx, uid, int(req.Msg.GetYear()))
	if err != nil {
		return nil, h.toConnectErr(ctx, "get year in review", err)
	}
	return connect.NewResponse(&recapv1.GetRecapResponse{Recap: rec}), nil
}

func (h *Handler) GetRecap(ctx context.Context, req *connect.Request[recapv1.GetRecapRequest]) (*connect.Response[recapv1.GetRecapResponse], error) {
	uid, id, err := h.owned(ctx, req.Msg.GetRecapId())
	if err != nil {
		return nil, err
	}
	rec, err := h.svc.Get(ctx, uid, id)
	if err != nil {
		return nil, h.toConnectErr(ctx, "get recap", err)
	}
	return connect.NewResponse(&recapv1.GetRecapResponse{Recap: rec}), nil
}

const defaultPageSize = 20

func (h *Handler) ListRecaps(ctx context.Context, req *connect.Request[recapv1.ListRecapsRequest]) (*connect.Response[recapv1.ListRecapsResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	limit := int(req.Msg.GetPageSize())
	if limit <= 0 {
		limit = defaultPageSize
	}
	var before time.Time
	if tok := req.Msg.GetPageToken(); tok != "" {
		before, err = time.Parse(time.RFC3339Nano, tok)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid page token"))
		}
	}
	list, next, err := h.svc.List(ctx, uid, limit, before)
	if err != nil {
		return nil, h.toConnectErr(ctx, "list recaps", err)
	}
	resp := &recapv1.ListRecapsResponse{Recaps: list}
	if !next.IsZero() {
		resp.NextPageToken = next.Format(time.RFC3339Nano)
	}
	return connect.NewResponse(resp), nil
}

func (h *Handler) ShareRecap(ctx context.Context, req *connect.Request[recapv1.ShareRecapRequest]) (*connect.Response[recapv1.ShareRecapResponse], error) {
	uid, id, err := h.owned(ctx, req.Msg.GetRecapId())
	if err != nil {
		return nil, err
	}
	code, url, err := h.svc.Share(ctx, uid, id)
	if err != nil {
		return nil, h.toConnectErr(ctx, "share recap", err)
	}
	return connect.NewResponse(&recapv1.ShareRecapResponse{ShareCode: code, ShareUrl: url}), nil
}

func (h *Handler) UnshareRecap(ctx context.Context, req *connect.Request[recapv1.UnshareRecapRequest]) (*connect.Response[recapv1.UnshareRecapResponse], error) {
	uid, id, err := h.owned(ctx, req.Msg.GetRecapId())
	if err != nil {
		return nil, err
	}
	if err := h.svc.Unshare(ctx, uid, id); err != nil {
		return nil, h.toConnectErr(ctx, "unshare recap", err)
	}
	return connect.NewResponse(&recapv1.UnshareRecapResponse{}), nil
}

func (h *Handler) MarkRecapViewed(ctx context.Context, req *connect.Request[recapv1.MarkRecapViewedRequest]) (*connect.Response[recapv1.MarkRecapViewedResponse], error) {
	uid, id, err := h.owned(ctx, req.Msg.GetRecapId())
	if err != nil {
		return nil, err
	}
	if err := h.svc.MarkViewed(ctx, uid, id); err != nil {
		return nil, h.toConnectErr(ctx, "mark recap viewed", err)
	}
	return connect.NewResponse(&recapv1.MarkRecapViewedResponse{}), nil
}

func (h *Handler) RenderRecapCardImage(ctx context.Context, req *connect.Request[recapv1.RenderRecapCardImageRequest]) (*connect.Response[recapv1.RenderRecapCardImageResponse], error) {
	uid, id, err := h.owned(ctx, req.Msg.GetRecapId())
	if err != nil {
		return nil, err
	}
	rec, err := h.svc.Owned(ctx, uid, id)
	if err != nil {
		return nil, h.toConnectErr(ctx, "render recap card", err)
	}
	var png []byte
	w, ht := render.StoryW, render.StoryH
	if req.Msg.GetFormat() == recapv1.RecapImageFormat_RECAP_IMAGE_FORMAT_OG {
		png, err = render.OG(rec)
		w, ht = render.OGW, render.OGH
	} else {
		card := findCard(rec, req.Msg.GetCardId())
		if card == nil {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("card not found"))
		}
		png, err = render.Card(rec, card)
	}
	if err != nil {
		return nil, h.toConnectErr(ctx, "render recap card", err)
	}
	return connect.NewResponse(&recapv1.RenderRecapCardImageResponse{Png: png, Width: int32(w), Height: int32(ht)}), nil
}

func (h *Handler) GetSharedRecap(ctx context.Context, req *connect.Request[recapv1.GetSharedRecapRequest]) (*connect.Response[recapv1.GetRecapResponse], error) {
	rec, err := h.svc.Shared(ctx, req.Msg.GetShareCode(), true)
	if err != nil {
		return nil, h.toConnectErr(ctx, "get shared recap", err)
	}
	return connect.NewResponse(&recapv1.GetRecapResponse{Recap: rec}), nil
}

func (h *Handler) owned(ctx context.Context, rawID string) (uuid.UUID, uuid.UUID, error) {
	uid, err := userID(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid recap ID"))
	}
	return uid, id, nil
}

func findCard(rec *recapv1.Recap, id string) *recapv1.RecapCard {
	for _, c := range rec.GetCards() {
		if c.GetId() == id {
			return c
		}
	}
	return nil
}
