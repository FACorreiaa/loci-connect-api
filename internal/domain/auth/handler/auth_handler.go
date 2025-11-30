package handler

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	auth "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/auth"
	authconnect "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/auth/authconnect"
	commonpb "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/common"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/presenter"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/service"
)

// AuthHandler implements the AuthService Connect handlers.
type AuthHandler struct {
	authconnect.UnimplementedAuthServiceHandler
	service *service.AuthService
}

// NewAuthHandler constructs a new handler.
func NewAuthHandler(svc *service.AuthService) *AuthHandler {
	return &AuthHandler{
		service: svc,
	}
}

// Register handles user registration RPCs.
func (h *AuthHandler) Register(
	ctx context.Context,
	req *connect.Request[auth.RegisterRequest],
) (*connect.Response[commonpb.Response], error) {
	if req.Msg.Email == "" || string(req.Msg.Password) == "" || req.Msg.Username == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("email, username, and password are required"))
	}

	result, err := h.service.RegisterUser(ctx, service.RegisterParams{
		Email:       req.Msg.Email,
		Username:    req.Msg.Username,
		Password:    string(req.Msg.Password),
		DisplayName: req.Msg.Username,
		Metadata:    metadataFromRequest(req),
	})
	if err != nil {
		return nil, h.toConnectError(err)
	}

	return connect.NewResponse(presenter.RegisterResponse(result)), nil
}

// Login authenticates a user.
func (h *AuthHandler) Login(ctx context.Context, req *connect.Request[auth.LoginRequest]) (*connect.Response[auth.LoginResponse], error) {
	if req.Msg.Email == "" || string(req.Msg.Password) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("email and password are required"))
	}

	result, err := h.service.Login(ctx, service.LoginParams{
		Email:    req.Msg.Email,
		Password: req.Msg.Password,
		Metadata: metadataFromRequest(req),
	})
	if err != nil {
		return nil, h.toConnectError(err)
	}

	return connect.NewResponse(presenter.LoginResponse(result)), nil
}

// RefreshToken issues new access/refresh tokens.
func (h *AuthHandler) RefreshToken(ctx context.Context, req *connect.Request[auth.RefreshTokenRequest]) (*connect.Response[auth.TokenResponse], error) {
	if req.Msg.RefreshToken == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("refresh token is required"))
	}

	tokens, err := h.service.RefreshTokens(ctx, service.RefreshTokenParams{
		RefreshToken: req.Msg.RefreshToken,
		Metadata:     metadataFromRequest(req),
	})
	if err != nil {
		return nil, h.toConnectError(err)
	}

	return connect.NewResponse(presenter.RefreshTokenResponse(tokens)), nil
}

// ValidateSession validates a session (treated as an access token here).
func (h *AuthHandler) ValidateSession(ctx context.Context, req *connect.Request[auth.ValidateSessionRequest]) (*connect.Response[auth.ValidateSessionResponse], error) {
	if req.Msg.SessionId == "" {
		return connect.NewResponse(&auth.ValidateSessionResponse{Valid: false}), nil
	}

	claims, err := h.service.ValidateAccessToken(ctx, req.Msg.SessionId)
	if err != nil {
		return connect.NewResponse(&auth.ValidateSessionResponse{Valid: false}), nil
	}

	return connect.NewResponse(presenter.ValidateSessionResponse(claims)), nil
}

// ChangePassword is not yet implemented.
func (h *AuthHandler) ChangePassword(_ context.Context, _ *connect.Request[auth.ChangePasswordRequest]) (*connect.Response[commonpb.Response], error) {
	return nil, connect.NewError(
		connect.CodeUnimplemented,
		errors.New("change password is not implemented"),
	)
}

// ChangeEmail is not yet implemented.
func (h *AuthHandler) ChangeEmail(_ context.Context, _ *connect.Request[auth.ChangeEmailRequest]) (*connect.Response[commonpb.Response], error) {
	return nil, connect.NewError(
		connect.CodeUnimplemented,
		errors.New("change email is not implemented"),
	)
}

// Logout deletes the refresh token session.
func (h *AuthHandler) Logout(ctx context.Context, req *connect.Request[auth.LogoutRequest]) (*connect.Response[commonpb.Response], error) {
	if req.Msg.RefreshToken == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("refresh token is required"))
	}

	if err := h.service.Logout(ctx, req.Msg.RefreshToken); err != nil {
		return nil, h.toConnectError(err)
	}

	msg := "Logged out successfully"
	return connect.NewResponse(&commonpb.Response{
		Success: true,
		Message: &msg,
	}), nil
}

func metadataFromRequest[T any](req *connect.Request[T]) service.SessionMetadata {
	return service.SessionMetadata{
		UserAgent: req.Header().Get("User-Agent"),
		ClientIP:  req.Peer().Addr,
	}
}

func (h *AuthHandler) toConnectError(err error) error {
	switch {
	case errors.Is(err, common.ErrUserAlreadyExists):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, common.ErrInvalidCredentials):
		return connect.NewError(connect.CodeUnauthenticated, err)
	case errors.Is(err, common.ErrInvalidToken), errors.Is(err, common.ErrSessionNotFound):
		return connect.NewError(connect.CodeUnauthenticated, err)
	case errors.Is(err, common.ErrUserNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, service.ErrAccountInactive):
		return connect.NewError(connect.CodePermissionDenied, err)
	case errors.Is(err, service.ErrPasswordTooShort),
		errors.Is(err, service.ErrPasswordNoDigit),
		errors.Is(err, service.ErrPasswordNoLowercase),
		errors.Is(err, service.ErrPasswordNoUppercase),
		errors.Is(err, service.ErrPasswordNoSpecial):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
