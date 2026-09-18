package handler

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	commonpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/common"
	interestv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/interest"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/interest/interestconnect"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/interests"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/interests/presenter"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
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
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	interestsList, err := h.service.GetAllInterests(ctx, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// active_only was declared in the proto and discarded here — the parameter
	// was literally named _. Deactivating an interest therefore looked like
	// nothing had happened, because the very next fetch returned it again.
	//
	// The filter is applied here rather than in SQL because the list is one
	// row per global interest plus the user's own, and pushing it down would
	// mean changing the repository and service signatures and their mocks for
	// a list this size.
	if req.Msg.GetActiveOnly() {
		active := make([]*locitypes.Interest, 0, len(interestsList))
		for _, i := range interestsList {
			if i != nil && i.Active != nil && *i.Active {
				active = append(active, i)
			}
		}
		interestsList = active
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

// DeleteInterest removes one of the caller's own custom interests.
//
// Deactivating and deleting are different things and only the first existed, so
// the client's delete button called UpdateInterest with active=false, said
// "Interest deleted successfully!", and left the interest in the list. The
// service and repository have always had a real user-scoped DELETE behind
// RemoveInterests; there was no RPC reaching it.
func (h *InterestHandler) DeleteInterest(ctx context.Context, req *connect.Request[interestv1.DeleteInterestRequest]) (*connect.Response[commonpb.Response], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	interestID, err := uuid.Parse(req.Msg.GetInterestId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Scoped by user id in the repository, so another user's interest id is a
	// no-op that reports not-found rather than deleting anything.
	if err := h.service.RemoveInterests(ctx, userID, interestID); err != nil {
		if errors.Is(err, locitypes.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	msg := "interest deleted"
	return connect.NewResponse(&commonpb.Response{Success: true, Message: &msg}), nil
}
