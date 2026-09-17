package localcontext

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	lcv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/localcontext"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// WithNewsTicker attaches the breaking-news strip. Optional: without it the
// RPC answers "disabled", which the client renders as nothing.
func (h *Handler) WithNewsTicker(s *NewsTickerService) *Handler {
	h.news = s
	return h
}

func newsUserID(ctx context.Context) (uuid.UUID, error) {
	raw, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("sign in to see your news"))
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid session"))
	}
	return id, nil
}

func (h *Handler) GetNewsTicker(
	ctx context.Context,
	req *connect.Request[lcv1.GetNewsTickerRequest],
) (*connect.Response[lcv1.GetNewsTickerResponse], error) {
	userID, err := newsUserID(ctx)
	if err != nil {
		return nil, err
	}
	if h.news == nil {
		return connect.NewResponse(&lcv1.GetNewsTickerResponse{Enabled: false}), nil
	}
	ticker, err := h.news.Ticker(ctx, userID, int(req.Msg.GetLimit()))
	if err != nil {
		// Same rule as GetLocalContext: a nicety never fails the desk.
		h.logger.WarnContext(ctx, "news ticker failed; returning disabled", slog.Any("error", err))
		return connect.NewResponse(&lcv1.GetNewsTickerResponse{Enabled: true, Stale: true}), nil
	}
	out := &lcv1.GetNewsTickerResponse{Enabled: ticker.Enabled, Stale: ticker.Stale, CountryCodes: ticker.CountryCodes}
	for _, it := range ticker.Items {
		out.Items = append(out.Items, &lcv1.NewsTickerItem{
			Id: it.ID, Title: it.Title, Url: it.URL, Source: it.Source,
			PublishedAt: timestamppb.New(it.PublishedAt), CountryCode: it.CountryCode,
		})
	}
	return connect.NewResponse(out), nil
}

func (h *Handler) SetNewsTickerEnabled(
	ctx context.Context,
	req *connect.Request[lcv1.SetNewsTickerEnabledRequest],
) (*connect.Response[lcv1.SetNewsTickerEnabledResponse], error) {
	userID, err := newsUserID(ctx)
	if err != nil {
		return nil, err
	}
	if h.news == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("the news ticker is not configured on this server"))
	}
	if err := h.news.SetEnabled(ctx, userID, req.Msg.GetEnabled()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&lcv1.SetNewsTickerEnabledResponse{Enabled: req.Msg.GetEnabled()}), nil
}
