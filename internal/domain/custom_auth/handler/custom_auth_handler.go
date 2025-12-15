package handler

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"github.com/markbates/goth"

	customauth "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/custom_auth"
	customauthconnect "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/custom_auth/customauthconnect"

	authservice "github.com/FACorreiaa/loci-connect-api/internal/domain/auth/service"
	cacommon "github.com/FACorreiaa/loci-connect-api/internal/domain/custom_auth/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/custom_auth/service"
)

// CustomAuthHandler implements the CustomAuthService Connect handlers
type CustomAuthHandler struct {
	customauthconnect.UnimplementedCustomAuthServiceHandler
	oauthService *service.OAuthService
	phoneService *service.PhoneService
	authService  *authservice.AuthService
}

// NewCustomAuthHandler creates a new handler for custom authentication methods
func NewCustomAuthHandler(
	oauthSvc *service.OAuthService,
	phoneSvc *service.PhoneService,
	authSvc *authservice.AuthService,
) *CustomAuthHandler {
	return &CustomAuthHandler{
		oauthService: oauthSvc,
		phoneService: phoneSvc,
		authService:  authSvc,
	}
}

// GetOAuthURL returns the OAuth authorization URL for the specified provider
func (h *CustomAuthHandler) GetOAuthURL(
	ctx context.Context,
	req *connect.Request[customauth.GetOAuthURLRequest],
) (*connect.Response[customauth.GetOAuthURLResponse], error) {
	provider := providerToString(req.Msg.Provider)

	if !h.oauthService.IsProviderConfigured(provider) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, cacommon.ErrOAuthProviderNotConfigured)
	}

	authURL, state, err := h.oauthService.GetAuthURL(provider, req.Msg.RedirectUri)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&customauth.GetOAuthURLResponse{
		AuthUrl: authURL,
		State:   state,
	}), nil
}

// OAuthCallback handles the OAuth callback and returns authentication tokens
func (h *CustomAuthHandler) OAuthCallback(
	ctx context.Context,
	req *connect.Request[customauth.OAuthCallbackRequest],
) (*connect.Response[customauth.OAuthCallbackResponse], error) {
	provider := providerToString(req.Msg.Provider)

	if !h.oauthService.IsProviderConfigured(provider) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, cacommon.ErrOAuthProviderNotConfigured)
	}

	// Complete OAuth and get user info from provider
	gothUser, err := h.oauthService.CompleteAuth(provider, req.Msg.Code, req.Msg.State)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	// Get session metadata from request
	meta := authservice.SessionMetadata{
		UserAgent: req.Header().Get("User-Agent"),
		ClientIP:  req.Peer().Addr,
	}

	// Find or create user via auth service
	result, isNew, err := h.authService.LoginOrRegisterOAuth(ctx, provider, gothUser, meta)
	if err != nil {
		return nil, h.toConnectError(err)
	}

	return connect.NewResponse(&customauth.OAuthCallbackResponse{
		AccessToken:  result.Tokens.AccessToken,
		RefreshToken: result.Tokens.RefreshToken,
		UserId:       result.User.ID.String(),
		Email:        result.User.Email,
		Username:     result.User.Username,
		IsNewUser:    isNew,
	}), nil
}

// SendPhoneVerification sends a verification code via SMS
func (h *CustomAuthHandler) SendPhoneVerification(
	ctx context.Context,
	req *connect.Request[customauth.SendPhoneVerificationRequest],
) (*connect.Response[customauth.SendPhoneVerificationResponse], error) {
	if !h.phoneService.IsEnabled() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("phone verification is not configured"))
	}

	if err := h.phoneService.SendVerification(req.Msg.PhoneNumber); err != nil {
		return nil, connect.NewError(connect.CodeInternal, cacommon.ErrPhoneSendFailed)
	}

	return connect.NewResponse(&customauth.SendPhoneVerificationResponse{
		Success: true,
		Message: "Verification code sent successfully",
	}), nil
}

// VerifyPhone verifies the phone code and returns authentication tokens
func (h *CustomAuthHandler) VerifyPhone(
	ctx context.Context,
	req *connect.Request[customauth.VerifyPhoneRequest],
) (*connect.Response[customauth.VerifyPhoneResponse], error) {
	if !h.phoneService.IsEnabled() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("phone verification is not configured"))
	}

	// Verify the code with Twilio
	valid, err := h.phoneService.CheckVerification(req.Msg.PhoneNumber, req.Msg.Code)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if !valid {
		return nil, connect.NewError(connect.CodeUnauthenticated, cacommon.ErrInvalidVerificationCode)
	}

	// Get session metadata from request
	meta := authservice.SessionMetadata{
		UserAgent: req.Header().Get("User-Agent"),
		ClientIP:  req.Peer().Addr,
	}

	// Find or create user by phone number
	result, isNew, err := h.authService.LoginOrRegisterPhone(ctx, req.Msg.PhoneNumber, meta)
	if err != nil {
		return nil, h.toConnectError(err)
	}

	return connect.NewResponse(&customauth.VerifyPhoneResponse{
		AccessToken:  result.Tokens.AccessToken,
		RefreshToken: result.Tokens.RefreshToken,
		UserId:       result.User.ID.String(),
		IsNewUser:    isNew,
	}), nil
}

// providerToString converts the OAuthProvider enum to a lowercase string
func providerToString(p customauth.OAuthProvider) string {
	name := p.String()
	// Remove "OAUTH_PROVIDER_" prefix and convert to lowercase
	name = strings.TrimPrefix(name, "OAUTH_PROVIDER_")
	return strings.ToLower(name)
}

// toConnectError converts domain errors to Connect errors
func (h *CustomAuthHandler) toConnectError(err error) error {
	switch {
	case errors.Is(err, authservice.ErrAccountInactive):
		return connect.NewError(connect.CodePermissionDenied, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

// Ensure AuthService has the required methods for OAuth/Phone login
// These will be added in the next step to auth_service.go
var _ interface {
	LoginOrRegisterOAuth(ctx context.Context, provider string, gothUser *goth.User, meta authservice.SessionMetadata) (*authservice.LoginResult, bool, error)
	LoginOrRegisterPhone(ctx context.Context, phone string, meta authservice.SessionMetadata) (*authservice.LoginResult, bool, error)
} = (*authservice.AuthService)(nil)
