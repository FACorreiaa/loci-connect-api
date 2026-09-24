package watch

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat/chatconnect"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/presenter"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Handler serves loci.chat.WatchService.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

var _ chatconnect.WatchServiceHandler = (*Handler)(nil)

func (h *Handler) ProposeWatch(ctx context.Context, req *connect.Request[chatv1.ProposeWatchRequest]) (*connect.Response[chatv1.ProposeWatchResponse], error) {
	if _, err := callerID(ctx); err != nil {
		return nil, err
	}
	p, err := h.svc.Propose(ctx, req.Msg.GetText(), req.Msg.GetTimezone())
	if err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&chatv1.ProposeWatchResponse{Proposal: proposalToProto(p)}), nil
}

func (h *Handler) CreateWatch(ctx context.Context, req *connect.Request[chatv1.CreateWatchRequest]) (*connect.Response[chatv1.CreateWatchResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	sessionID, err := uuid.Parse(req.Msg.GetSessionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session id"))
	}
	if req.Msg.GetProposal() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("proposal is required"))
	}
	w, msg, err := h.svc.Create(ctx, userID, sessionID, proposalFromProto(req.Msg.GetProposal()))
	if err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&chatv1.CreateWatchResponse{
		Watch:        watchToProto(w),
		Confirmation: presenter.ToConversationMessage(msg),
	}), nil
}

func (h *Handler) ListWatches(ctx context.Context, req *connect.Request[chatv1.ListWatchesRequest]) (*connect.Response[chatv1.ListWatchesResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	var sessionID *uuid.UUID
	if raw := req.Msg.GetSessionId(); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session id"))
		}
		sessionID = &id
	}
	ws, err := h.svc.List(ctx, userID, sessionID)
	if err != nil {
		return nil, toConnectError(err)
	}
	out := make([]*chatv1.Watch, 0, len(ws))
	for _, w := range ws {
		out = append(out, watchToProto(w))
	}
	return connect.NewResponse(&chatv1.ListWatchesResponse{Watches: out}), nil
}

func (h *Handler) DeleteWatch(ctx context.Context, req *connect.Request[chatv1.DeleteWatchRequest]) (*connect.Response[chatv1.DeleteWatchResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid watch id"))
	}
	if err := h.svc.Delete(ctx, userID, id); err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&chatv1.DeleteWatchResponse{}), nil
}

func callerID(ctx context.Context) (uuid.UUID, error) {
	raw, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || raw == "" {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid user id: %w", err))
	}
	return id, nil
}

func toConnectError(err error) error {
	switch {
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrUnknownZone):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrSessionAbsent):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, ErrLimitReached):
		return connect.NewError(connect.CodeResourceExhausted, err)
	default:
		// Storage errors carry SQL detail; keep it in the logs, not the wire.
		return connect.NewError(connect.CodeInternal, errors.New("standing tasks are unavailable right now"))
	}
}

func proposalToProto(p Proposal) *chatv1.WatchProposal {
	out := &chatv1.WatchProposal{
		Title:           p.Title,
		ScheduleHuman:   p.ScheduleHuman,
		IntervalMinutes: int32(p.IntervalMinutes), //nolint:gosec // bounded by MaxIntervalMinutes
		Spec:            p.Spec,
	}
	if p.FirstRunAt != nil {
		out.FirstRunAt = timestamppb.New(*p.FirstRunAt)
	}
	return out
}

func proposalFromProto(p *chatv1.WatchProposal) Proposal {
	out := Proposal{
		Title:           p.GetTitle(),
		ScheduleHuman:   p.GetScheduleHuman(),
		IntervalMinutes: int(p.GetIntervalMinutes()),
		Spec:            p.GetSpec(),
	}
	if p.GetFirstRunAt() != nil {
		t := p.GetFirstRunAt().AsTime()
		out.FirstRunAt = &t
	}
	return out
}

func watchToProto(w Watch) *chatv1.Watch {
	out := &chatv1.Watch{
		Id:              w.ID.String(),
		SessionId:       w.SessionID.String(),
		Title:           w.Title,
		ScheduleHuman:   w.ScheduleHuman,
		IntervalMinutes: int32(w.IntervalMinutes), //nolint:gosec // bounded by the table CHECK
		Spec:            w.Spec,
		Enabled:         w.Enabled,
		NextRunAt:       timestamppb.New(w.NextRunAt),
		CreatedAt:       timestamppb.New(w.CreatedAt),
	}
	if w.LastRunAt != nil {
		out.LastRunAt = timestamppb.New(*w.LastRunAt)
	}
	return out
}
