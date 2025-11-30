package presenter

import (
	auth "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/auth"
	commonpb "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/common"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/service"
)

// RegisterResponse converts a register result into a generic response.
func RegisterResponse(result *service.RegisterResult) *commonpb.Response {
	if result == nil {
		return &commonpb.Response{}
	}

	msg := "Registration successful"
	if result.EmailVerificationRequired {
		msg = "Registration successful. Please verify your email."
	}

	return &commonpb.Response{
		Success: true,
		Message: &msg,
	}
}

// LoginResponse converts a login result into its RPC response.
func LoginResponse(result *service.LoginResult) *auth.LoginResponse {
	if result == nil {
		return &auth.LoginResponse{}
	}

	return &auth.LoginResponse{
		AccessToken:  result.Tokens.AccessToken,
		RefreshToken: result.Tokens.RefreshToken,
		Username:     result.User.Username,
		UserId:       result.User.ID.String(),
		Email:        result.User.Email,
		Message:      "Login successful",
	}
}

// RefreshTokenResponse renders a token pair as RPC response.
func RefreshTokenResponse(tokens *service.TokenPair) *auth.TokenResponse {
	if tokens == nil {
		return &auth.TokenResponse{}
	}

	return &auth.TokenResponse{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
	}
}

// ValidateSessionResponse renders claims into a session validation response.
func ValidateSessionResponse(claims *service.Claims) *auth.ValidateSessionResponse {
	if claims == nil {
		return &auth.ValidateSessionResponse{Valid: false}
	}

	return &auth.ValidateSessionResponse{
		Valid:    true,
		UserId:   &claims.UserID,
		Username: &claims.Username,
		Email:    &claims.Email,
	}
}
