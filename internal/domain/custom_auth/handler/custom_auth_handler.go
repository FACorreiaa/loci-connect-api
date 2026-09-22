package handler

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"connectrpc.com/connect"
	"github.com/markbates/goth"

	customauth "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/custom_auth"
	customauthconnect "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/custom_auth/customauthconnect"

	authservice "github.com/FACorreiaa/loci-connect-api/internal/domain/auth/service"
	cacommon "github.com/FACorreiaa/loci-connect-api/internal/domain/custom_auth/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/custom_auth/service"
)

// CustomAuthHandler implements the CustomAuthService Connect handlers
type CustomAuthHandler struct {
	customauthconnect.UnimplementedCustomAuthServiceHandler
	oauthService    *service.OAuthService
	idTokenVerifier *service.IDTokenVerifier
	phoneService    *service.PhoneService
	authService     *authservice.AuthService
}

// NewCustomAuthHandler creates a new handler for custom authentication methods
func NewCustomAuthHandler(
	oauthSvc *service.OAuthService,
	idTokenVerifier *service.IDTokenVerifier,
	phoneSvc *service.PhoneService,
	authSvc *authservice.AuthService,
) *CustomAuthHandler {
	return &CustomAuthHandler{
		oauthService:    oauthSvc,
		idTokenVerifier: idTokenVerifier,
		phoneService:    phoneSvc,
		authService:     authSvc,
	}
}

// GetOAuthURL returns the OAuth authorization URL for the specified provider
func (h *CustomAuthHandler) GetOAuthURL(
	_ context.Context,
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

// SignInWithIDToken signs in with an ID token from a native sheet (iOS Apple
// and Google). The token replaces the code exchange; everything after it —
// finding or creating the account, issuing a session — is the OAuth path.
func (h *CustomAuthHandler) SignInWithIDToken(
	ctx context.Context,
	req *connect.Request[customauth.SignInWithIDTokenRequest],
) (*connect.Response[customauth.OAuthCallbackResponse], error) {
	provider := providerToString(req.Msg.Provider)

	if !h.idTokenVerifier.IsConfigured(provider) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, cacommon.ErrOAuthProviderNotConfigured)
	}

	claims, err := h.idTokenVerifier.Verify(ctx, provider, req.Msg.IdToken, req.Msg.Nonce)
	if err != nil {
		if errors.Is(err, service.ErrIDTokenInvalid) {
			slog.WarnContext(ctx, "native sign-in token refused",
				slog.String("provider", provider), slog.String("error", err.Error()))
			return nil, connect.NewError(connect.CodeUnauthenticated, service.ErrIDTokenInvalid)
		}
		// The provider's key endpoint was unreachable: not the caller's fault.
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}

	// Both providers put a verified email in every ID token. Without one,
	// LoginOrRegisterOAuth would look an unknown subject up by the empty
	// email and could link it to an account that has none, such as a phone
	// sign-up. Refuse instead.
	if claims.Email == "" {
		slog.WarnContext(ctx, "native sign-in token has no verified email", slog.String("provider", provider))
		return nil, connect.NewError(connect.CodeUnauthenticated, service.ErrIDTokenInvalid)
	}

	// Keyed on the subject, like the web flow, so this is the same account.
	gothUser := &goth.User{
		Provider: provider,
		UserID:   claims.Subject,
		Email:    claims.Email,
		Name:     strings.TrimSpace(req.Msg.FullName),
	}

	meta := authservice.SessionMetadata{
		UserAgent: req.Header().Get("User-Agent"),
		ClientIP:  req.Peer().Addr,
	}

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
	_ context.Context,
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
