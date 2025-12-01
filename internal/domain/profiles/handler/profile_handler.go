package handler

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"

	commonpb "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/common"
	profilev1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/profile"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/profile/profileconnect"

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

func (h *ProfileHandler) GetUserPreferenceProfiles(ctx context.Context, req *connect.Request[profilev1.GetUserPreferenceProfilesRequest]) (*connect.Response[profilev1.GetUserPreferenceProfilesResponse], error) {
	userIDStr := req.Msg.GetUserId()
	if userIDStr == "" {
		var ok bool
		userIDStr, ok = interceptors.GetUserIDFromContext(ctx)
		if !ok || userIDStr == "" {
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
		}
	}
	userID, err := presenter.ParseUUID(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid user id: %w", err))
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
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := presenter.ParseUUID(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid user id: %w", err))
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
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := presenter.ParseUUID(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid user id: %w", err))
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
