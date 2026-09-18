package handler

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	commonpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/common"
	profilev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/profile"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/profiles/presenter"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// The RPCs below were declared in the contract and left to the embedded
// UnimplementedProfileServiceHandler, so they answered CodeUnimplemented -- while
// the client shipped hooks and mutations for every one of them. The service
// methods they need already existed.

// callerAndProfile resolves the authenticated user and the requested profile id
// together, since every one of these RPCs needs exactly that pair.
func callerAndProfile(ctx context.Context, profileID string) (uuid.UUID, uuid.UUID, error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid user id: %w", err))
	}
	pid, err := uuid.Parse(profileID)
	if err != nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid profile id: %w", err))
	}
	return userID, pid, nil
}

// asConnectError maps a domain error onto the closest Connect code, so a missing
// profile reads as NotFound rather than an opaque Internal.
//
// The cases that matter are the ones the person can act on. Mapping everything
// to Internal makes "that profile is gone" and "the database is down" look
// identical to the client, and hides refusals the UI could explain -- deleting
// your default profile is a thing you fix by making another one default first.
func asConnectError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, locitypes.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, locitypes.ErrBadRequest):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, locitypes.ErrForbidden):
		return connect.NewError(connect.CodePermissionDenied, err)
	case errors.Is(err, locitypes.ErrConflict):
		return connect.NewError(connect.CodeAlreadyExists, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

func (h *ProfileHandler) GetUserPreferenceProfile(ctx context.Context, req *connect.Request[profilev1.GetUserPreferenceProfileRequest]) (*connect.Response[profilev1.GetUserPreferenceProfileResponse], error) {
	userID, profileID, err := callerAndProfile(ctx, req.Msg.GetProfileId())
	if err != nil {
		return nil, err
	}

	profile, err := h.service.GetSearchProfile(ctx, userID, profileID)
	if err != nil {
		return nil, asConnectError(err)
	}

	return connect.NewResponse(&profilev1.GetUserPreferenceProfileResponse{
		Profile: presenter.ToProtoProfile(*profile),
	}), nil
}

func (h *ProfileHandler) DeleteUserPreferenceProfile(ctx context.Context, req *connect.Request[profilev1.DeleteUserPreferenceProfileRequest]) (*connect.Response[commonpb.Response], error) {
	userID, profileID, err := callerAndProfile(ctx, req.Msg.GetProfileId())
	if err != nil {
		return nil, err
	}

	if err := h.service.DeleteSearchProfile(ctx, userID, profileID); err != nil {
		return nil, asConnectError(err)
	}

	msg := "profile deleted"
	return connect.NewResponse(&commonpb.Response{Success: true, Message: &msg}), nil
}

func (h *ProfileHandler) SetDefaultProfile(ctx context.Context, req *connect.Request[profilev1.SetDefaultProfileRequest]) (*connect.Response[commonpb.Response], error) {
	userID, profileID, err := callerAndProfile(ctx, req.Msg.GetProfileId())
	if err != nil {
		return nil, err
	}

	if err := h.service.SetDefaultSearchProfile(ctx, userID, profileID); err != nil {
		return nil, asConnectError(err)
	}

	msg := "default profile set"
	return connect.NewResponse(&commonpb.Response{Success: true, Message: &msg}), nil
}

// domainPreferences reads the profile once; GetSearchProfile already attaches all
// four blobs, so each getter is a projection of the same read rather than its own
// query.
func (h *ProfileHandler) domainPreferences(ctx context.Context, profileID string) (*locitypes.UserPreferenceProfileResponse, error) {
	userID, pid, err := callerAndProfile(ctx, profileID)
	if err != nil {
		return nil, err
	}
	profile, err := h.service.GetSearchProfile(ctx, userID, pid)
	if err != nil {
		return nil, asConnectError(err)
	}
	return profile, nil
}

func (h *ProfileHandler) GetAccommodationPreferences(ctx context.Context, req *connect.Request[profilev1.GetDomainPreferencesRequest]) (*connect.Response[profilev1.AccommodationPreferencesResponse], error) {
	profile, err := h.domainPreferences(ctx, req.Msg.GetProfileId())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&profilev1.AccommodationPreferencesResponse{
		Success:     true,
		Preferences: presenter.ToProtoAccommodationPreferences(profile.AccommodationPreferences),
	}), nil
}

func (h *ProfileHandler) GetDiningPreferences(ctx context.Context, req *connect.Request[profilev1.GetDomainPreferencesRequest]) (*connect.Response[profilev1.DiningPreferencesResponse], error) {
	profile, err := h.domainPreferences(ctx, req.Msg.GetProfileId())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&profilev1.DiningPreferencesResponse{
		Success:     true,
		Preferences: presenter.ToProtoDiningPreferences(profile.DiningPreferences),
	}), nil
}

func (h *ProfileHandler) GetActivityPreferences(ctx context.Context, req *connect.Request[profilev1.GetDomainPreferencesRequest]) (*connect.Response[profilev1.ActivityPreferencesResponse], error) {
	profile, err := h.domainPreferences(ctx, req.Msg.GetProfileId())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&profilev1.ActivityPreferencesResponse{
		Success:     true,
		Preferences: presenter.ToProtoActivityPreferences(profile.ActivityPreferences),
	}), nil
}

func (h *ProfileHandler) GetItineraryPreferences(ctx context.Context, req *connect.Request[profilev1.GetDomainPreferencesRequest]) (*connect.Response[profilev1.ItineraryPreferencesResponse], error) {
	profile, err := h.domainPreferences(ctx, req.Msg.GetProfileId())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&profilev1.ItineraryPreferencesResponse{
		Success:     true,
		Preferences: presenter.ToProtoItineraryPreferences(profile.ItineraryPreferences),
	}), nil
}

func (h *ProfileHandler) GetCombinedFilters(ctx context.Context, req *connect.Request[profilev1.GetCombinedFiltersRequest]) (*connect.Response[profilev1.CombinedFiltersResponse], error) {
	profile, err := h.domainPreferences(ctx, req.Msg.GetProfileId())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&profilev1.CombinedFiltersResponse{
		Success:       true,
		Profile:       presenter.ToProtoProfile(*profile),
		Accommodation: presenter.ToProtoAccommodationPreferences(profile.AccommodationPreferences),
		Dining:        presenter.ToProtoDiningPreferences(profile.DiningPreferences),
		Activity:      presenter.ToProtoActivityPreferences(profile.ActivityPreferences),
		Itinerary:     presenter.ToProtoItineraryPreferences(profile.ItineraryPreferences),
	}), nil
}
