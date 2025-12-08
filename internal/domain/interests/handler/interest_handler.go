package handler

import (
	"context"

	"connectrpc.com/connect"
	commonpb "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/common"
	interestv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/interest"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/interest/interestconnect"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/interests"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/interests/presenter"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

type InterestHandler struct {
	interestconnect.UnimplementedInterestServiceHandler
	service interests.Service
}

func NewInterestHandler(svc interests.Service) *InterestHandler {
	return &InterestHandler{service: svc}
}

func (h *InterestHandler) GetInterests(ctx context.Context, req *connect.Request[interestv1.GetInterestsRequest]) (*connect.Response[interestv1.GetInterestsResponse], error) {
	interestsList, err := h.service.GetAllInterests(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&interestv1.GetInterestsResponse{
		Interests: presenter.ToInterestProtos(interestsList),
	}), nil
}

func (h *InterestHandler) CreateInterest(ctx context.Context, req *connect.Request[interestv1.CreateInterestRequest]) (*connect.Response[commonpb.Response], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	_, err := h.service.CreateInterest(ctx, req.Msg.Name, req.Msg.Description, req.Msg.Active, userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	msg := "interest created"
	return connect.NewResponse(&commonpb.Response{Success: true, Message: &msg}), nil
}

func (h *InterestHandler) UpdateInterest(ctx context.Context, req *connect.Request[interestv1.UpdateInterestRequest]) (*connect.Response[commonpb.Response], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	interestID, err := uuid.Parse(req.Msg.InterestId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	params, err := presenter.FromUpdateProto(req.Msg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if err := h.service.UpdateInterests(ctx, userID, interestID, params); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	msg := "interest updated"
	return connect.NewResponse(&commonpb.Response{Success: true, Message: &msg}), nil
}
