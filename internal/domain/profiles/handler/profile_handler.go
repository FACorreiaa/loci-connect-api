package handler

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	commonpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/common"
	profilev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/profile"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/profile/profileconnect"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/profiles"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/profiles/presenter"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

type ProfileHandler struct {
	profileconnect.UnimplementedProfileServiceHandler
	service profiles.Service
}

func NewProfileHandler(svc profiles.Service) *ProfileHandler {
	return &ProfileHandler{service: svc}
}

// callerID resolves the authenticated subject from the token claims.
//
// Every RPC on this service acts on the caller's own data. Requests carry a
// user_id field and several handlers used to read it, falling back to the
// token only when it was empty — which let any authenticated caller pass
// somebody else's id and read their travel profiles. The field is now ignored
// everywhere; the token is the only authority on who is asking.
func callerID(ctx context.Context) (uuid.UUID, error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := presenter.ParseUUID(userIDStr)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid user id: %w", err))
	}
	return userID, nil
}

func (h *ProfileHandler) GetUserPreferenceProfiles(ctx context.Context, req *connect.Request[profilev1.GetUserPreferenceProfilesRequest]) (*connect.Response[profilev1.GetUserPreferenceProfilesResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}

	profilesResp, err := h.service.GetSearchProfiles(ctx, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&profilev1.GetUserPreferenceProfilesResponse{
		Profiles: presenter.ToProtoProfiles(profilesResp),
	}), nil
}

func (h *ProfileHandler) CreateUserPreferenceProfile(ctx context.Context, req *connect.Request[profilev1.CreateUserPreferenceProfileRequest]) (*connect.Response[commonpb.Response], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}

	params, err := presenter.FromCreateProto(req.Msg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if _, err := h.service.CreateSearchProfile(ctx, userID, params); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	msg := "profile created"
	return connect.NewResponse(&commonpb.Response{Success: true, Message: &msg}), nil
}

func (h *ProfileHandler) UpdateUserPreferenceProfile(ctx context.Context, req *connect.Request[profilev1.UpdateUserPreferenceProfileRequest]) (*connect.Response[commonpb.Response], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}

	profileID, err := presenter.ParseUUID(req.Msg.GetProfileId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid profile id: %w", err))
	}

	params, err := presenter.FromUpdateProto(req.Msg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if err := h.service.UpdateSearchProfile(ctx, userID, profileID, params); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	msg := "profile updated"
	return connect.NewResponse(&commonpb.Response{Success: true, Message: &msg}), nil
}
