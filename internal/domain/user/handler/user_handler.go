package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"

	commonpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/common"
	userpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/user"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/user/userconnect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/user"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/userdata"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// UserHandler implements the UserService Connect handlers.
type UserHandler struct {
	userconnect.UnimplementedUserServiceHandler
	service user.UserService
	// exporter assembles the full self-service data export. Optional: without
	// it the export falls back to the profile-only payload it used to return.
	exporter *userdata.Exporter
}

// NewUserHandler constructs a new handler.
func NewUserHandler(svc user.UserService) *UserHandler {
	return &UserHandler{
		service: svc,
	}
}

// SetExporter enables the complete data export. Without it the handler returns
// the profile alone, which is what it did before the exporter existed.
func (h *UserHandler) SetExporter(e *userdata.Exporter) {
	if h != nil {
		h.exporter = e
	}
}

// GetUserProfile retrieves the user's profile.
func (h *UserHandler) GetUserProfile(
	ctx context.Context,
	req *connect.Request[userpb.GetUserProfileRequest],
) (*connect.Response[userpb.GetUserProfileResponse], error) {
	// Get user ID from request or from context (authenticated user)
	userIDStr := req.Msg.GetUserId()
	if userIDStr == "" {
		var ok bool
		userIDStr, ok = interceptors.GetUserIDFromContext(ctx)
		if !ok || userIDStr == "" {
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
		}
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid user id: %w", err))
	}

	profile, err := h.service.GetUserProfile(ctx, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&userpb.GetUserProfileResponse{
		Profile: toProtoProfile(profile),
	}), nil
}

// UpdateUserProfile updates the user's profile.
func (h *UserHandler) UpdateUserProfile(
	ctx context.Context,
	req *connect.Request[userpb.UpdateUserProfileRequest],
) (*connect.Response[commonpb.Response], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid user id: %w", err))
	}

	params := fromUpdateProto(req.Msg.GetParams())
	if err := h.service.UpdateUserProfile(ctx, userID, params); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	msg := "profile updated"
	return connect.NewResponse(&commonpb.Response{Success: true, Message: &msg}), nil
}

// toProtoProfile converts a domain UserProfile to proto UserProfile.
func toProtoProfile(p *locitypes.UserProfile) *userpb.UserProfile {
	if p == nil {
		return nil
	}

	proto := &userpb.UserProfile{
		Id:        p.ID.String(),
		Email:     p.Email,
		IsActive:  p.IsActive,
		CreatedAt: timestamppb.New(p.CreatedAt),
		UpdatedAt: timestamppb.New(p.UpdatedAt),
	}

	if p.Username != nil {
		proto.Username = p.Username
	}
	if p.Firstname != nil {
		proto.Firstname = p.Firstname
	}
	if p.Lastname != nil {
		proto.Lastname = p.Lastname
	}
	if p.PhoneNumber != nil {
		proto.PhoneNumber = p.PhoneNumber
	}
	if p.Age != nil {
		proto.Age = ptrTo(int32(*p.Age))
	}
	if p.City != nil {
		proto.City = p.City
	}
	if p.Country != nil {
		proto.Country = p.Country
	}
	if p.AboutYou != nil {
		proto.AboutYou = p.AboutYou
	}
	if p.Bio != nil {
		proto.Bio = p.Bio
	}
	if p.Location != nil {
		proto.Location = p.Location
	}
	if p.DisplayName != nil {
		proto.DisplayName = p.DisplayName
	}
	if p.ProfileImageURL != nil {
		proto.ProfileImageUrl = p.ProfileImageURL
	}
	if p.Theme != nil {
		proto.Theme = p.Theme
	}
	if p.Language != nil {
		proto.Language = p.Language
	}
	if p.EmailVerifiedAt != nil {
		proto.EmailVerifiedAt = timestamppb.New(*p.EmailVerifiedAt)
	}
	if p.LastLoginAt != nil {
		proto.LastLoginAt = timestamppb.New(*p.LastLoginAt)
	}

	proto.JoinedDate = timestamppb.New(p.JoinedDate)
	proto.Interests = p.Interests
	proto.Badges = p.Badges

	return proto
}

// fromUpdateProto converts proto UpdateProfileParams to domain UpdateProfileParams.
func fromUpdateProto(p *userpb.UpdateProfileParams) locitypes.UpdateProfileParams {
	if p == nil {
		return locitypes.UpdateProfileParams{}
	}

	params := locitypes.UpdateProfileParams{}

	if p.Username != nil {
		params.Username = p.Username
	}
	if p.PhoneNumber != nil {
		params.PhoneNumber = p.PhoneNumber
	}
	if p.Email != nil {
		params.Email = p.Email
	}
	if p.DisplayName != nil {
		params.DisplayName = p.DisplayName
	}
	if p.ProfileImageUrl != nil {
		params.ProfileImageURL = p.ProfileImageUrl
	}
	if p.Firstname != nil {
		params.Firstname = p.Firstname
	}
	if p.Lastname != nil {
		params.Lastname = p.Lastname
	}
	if p.Age != nil {
		age := int(*p.Age)
		params.Age = &age
	}
	if p.City != nil {
		params.City = p.City
	}
	if p.Country != nil {
		params.Country = p.Country
	}
	if p.AboutYou != nil {
		params.AboutYou = p.AboutYou
	}
	if p.Location != nil {
		params.Location = p.Location
	}
	if len(p.Interests) > 0 {
		params.Interests = &p.Interests
	}
	if len(p.Badges) > 0 {
		params.Badges = &p.Badges
	}
	if p.Theme != nil {
		params.Theme = p.Theme
	}
	if p.Language != nil {
		params.Language = p.Language
	}

	return params
}

// ptrTo returns a pointer to the given value.
func ptrTo[T any](v T) *T {
	return &v
}

// ExportUserData returns a machine-readable copy of the caller's data (GDPR-style
// self-service). Currently the user profile; extend as more owned data is surfaced.
func (h *UserHandler) ExportUserData(
	ctx context.Context,
	_ *connect.Request[userpb.ExportUserDataRequest],
) (*connect.Response[userpb.ExportUserDataResponse], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid user id: %w", err))
	}

	profile, err := h.service.GetUserProfile(ctx, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var data []byte
	if h.exporter != nil {
		bundle, buildErr := h.exporter.Build(ctx, userID, profile)
		if buildErr != nil {
			return nil, connect.NewError(connect.CodeInternal, buildErr)
		}
		data, err = bundle.JSON()
	} else {
		// Fallback for a handler constructed without an exporter.
		payload := map[string]any{
			"exported_at": time.Now().UTC().Format(time.RFC3339),
			"profile":     profile,
		}
		data, err = json.MarshalIndent(payload, "", "  ")
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("marshal export: %w", err))
	}

	return connect.NewResponse(&userpb.ExportUserDataResponse{
		Data:        data,
		ContentType: "application/json",
		Filename:    "loci-data-export.json",
	}), nil
}

// DeleteAccount permanently deletes the caller's account. Gated on an explicit
// "DELETE" confirmation to guard against accidental calls. Irreversible.
func (h *UserHandler) DeleteAccount(
	ctx context.Context,
	req *connect.Request[userpb.DeleteAccountRequest],
) (*connect.Response[commonpb.Response], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	if req.Msg.GetConfirmation() != "DELETE" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(`confirmation must be "DELETE"`))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid user id: %w", err))
	}

	if err := h.service.DeleteAccount(ctx, userID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	msg := "account permanently deleted"
	return connect.NewResponse(&commonpb.Response{Success: true, Message: &msg}), nil
}
