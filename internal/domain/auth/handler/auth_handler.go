package handler

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	auth "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/auth"
	authconnect "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/auth/authconnect"
	commonpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/common"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/presenter"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/service"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// AuthHandler implements the AuthService Connect handlers.
type AuthHandler struct {
	authconnect.UnimplementedAuthServiceHandler
	service *service.AuthService
	logger  *slog.Logger

	// mfa is nil when MFA is not configured; the MFA RPCs then report
	// Unimplemented instead of panicking on a nil dependency.
	mfa MFAService
}

// NewAuthHandler constructs a new handler.
func NewAuthHandler(svc *service.AuthService, logger *slog.Logger) *AuthHandler {
	return &AuthHandler{
		service: svc,
		logger:  logger,
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

// ValidateSession reports whether the caller's session is still good.
//
// It authenticates from the Authorization header, which is the only thing that
// can work: the request's session_id field is capped at 200 characters by the
// proto validation rules, while an access token is ~370, so a client that sent
// its token here was rejected with InvalidArgument, and a client that sent the
// JWT's `jti` instead got Valid:false because a bare UUID is not a parseable
// token. Either way the RPC could never succeed, which made every page refresh
// look like a logout.
//
// session_id is still honoured when present and short enough to be a token, so
// older clients keep working.
func (h *AuthHandler) ValidateSession(ctx context.Context, req *connect.Request[auth.ValidateSessionRequest]) (*connect.Response[auth.ValidateSessionResponse], error) {
	if ctxClaims, err := interceptors.GetClaimsFromContext(ctx); err == nil && ctxClaims != nil {
		return connect.NewResponse(presenter.ValidateSessionResponse(&service.Claims{
			UserID:   ctxClaims.UserID,
			Email:    ctxClaims.Email,
			Username: ctxClaims.Username,
			Role:     ctxClaims.Role,
		})), nil
	}

	if req.Msg.SessionId == "" {
		return connect.NewResponse(&auth.ValidateSessionResponse{Valid: false}), nil
	}

	claims, err := h.service.ValidateAccessToken(ctx, req.Msg.SessionId)
	if err != nil {
		return connect.NewResponse(&auth.ValidateSessionResponse{Valid: false}), nil
	}

	return connect.NewResponse(presenter.ValidateSessionResponse(claims)), nil
}

// ChangePassword updates the caller's password after verifying their current one.
func (h *AuthHandler) ChangePassword(ctx context.Context, req *connect.Request[auth.ChangePasswordRequest]) (*connect.Response[commonpb.Response], error) {
	claims, err := interceptors.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	if req.Msg.OldPassword == "" || req.Msg.NewPassword == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("old password and new password are required"))
	}

	if err := h.service.ChangePassword(ctx, claims.UserID, req.Msg.OldPassword, req.Msg.NewPassword); err != nil {
		return nil, h.toConnectError(err)
	}

	msg := "Password changed successfully"
	return connect.NewResponse(&commonpb.Response{
		Success: true,
		Message: &msg,
	}), nil
}

// ChangeEmail updates the caller's email after verifying their current password.
func (h *AuthHandler) ChangeEmail(ctx context.Context, req *connect.Request[auth.ChangeEmailRequest]) (*connect.Response[commonpb.Response], error) {
	claims, err := interceptors.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	if req.Msg.NewEmail == "" || req.Msg.Password == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("password and new email are required"))
	}

	if err := h.service.ChangeEmail(ctx, claims.UserID, req.Msg.Password, req.Msg.NewEmail); err != nil {
		return nil, h.toConnectError(err)
	}

	// TODO: trigger an email-change verification once a dedicated mailer template
	// (e.g. SendEmailChangeConfirmation) is added to service.EmailSender.
	msg := "Email changed successfully"
	return connect.NewResponse(&commonpb.Response{
		Success: true,
		Message: &msg,
	}), nil
}

// ForgotPassword initiates the password reset flow.
func (h *AuthHandler) ForgotPassword(ctx context.Context, req *connect.Request[auth.ForgotPasswordRequest]) (*connect.Response[commonpb.Response], error) {
	if req.Msg.Email == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("email is required"))
	}

	// Always return success to prevent email enumeration attacks
	// The service will silently fail if the email doesn't exist
	// Deliberately ignored: the response must not reveal whether the address
	// exists, so a failure here looks identical to a success from outside.
	_ = h.service.RequestPasswordReset(ctx, req.Msg.Email)

	msg := "If an account exists with this email, you will receive a password reset link"
	return connect.NewResponse(&commonpb.Response{
		Success: true,
		Message: &msg,
	}), nil
}

// ResetPassword completes the password reset with a valid token.
func (h *AuthHandler) ResetPassword(ctx context.Context, req *connect.Request[auth.ResetPasswordRequest]) (*connect.Response[commonpb.Response], error) {
	if req.Msg.Token == "" || req.Msg.NewPassword == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("token and new password are required"))
	}

	if err := h.service.ResetPassword(ctx, req.Msg.Token, req.Msg.NewPassword); err != nil {
		return nil, h.toConnectError(err)
	}

	msg := "Password reset successfully"
	return connect.NewResponse(&commonpb.Response{
		Success: true,
		Message: &msg,
	}), nil
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
