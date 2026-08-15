package handler

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"connectrpc.com/connect"
	auth "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/auth"
	commonpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/common"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/presenter"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/service"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/mfa"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// MFAService is the slice of mfa.Service the handler needs.
type MFAService interface {
	Status(ctx context.Context, userID uuid.UUID, role string) (mfa.Status, error)
	BeginEnrollment(ctx context.Context, userID uuid.UUID, accountName string) (uri, secret string, err error)
	ConfirmEnrollment(ctx context.Context, userID uuid.UUID, code string) ([]string, error)
	Disable(ctx context.Context, userID uuid.UUID, role, code, recoveryCode string) error
	RegenerateRecoveryCodes(ctx context.Context, userID uuid.UUID, code string) ([]string, error)
}

// WithMFA attaches the MFA service. Without it the MFA RPCs report Unimplemented
// rather than failing in some less obvious way.
func (h *AuthHandler) WithMFA(svc MFAService) *AuthHandler {
	h.mfa = svc
	return h
}

// VerifyMFA completes a login that was challenged for a second factor.
//
// Unauthenticated by design — the caller has no access token yet, which is the
// entire point of step-up. The challenge token from Login is the only credential
// it accepts.
func (h *AuthHandler) VerifyMFA(
	ctx context.Context,
	req *connect.Request[auth.VerifyMFARequest],
) (*connect.Response[auth.LoginResponse], error) {
	if h.mfa == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("MFA is not configured"))
	}

	code := strings.TrimSpace(req.Msg.GetCode())
	recovery := strings.TrimSpace(req.Msg.GetRecoveryCode())

	if code == "" && recovery == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("a verification code or a recovery code is required"))
	}
	if code != "" && recovery != "" {
		// Accepting both would make it ambiguous which factor was actually
		// checked, and which one to consume.
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("send either a verification code or a recovery code, not both"))
	}

	result, err := h.service.CompleteMFALogin(ctx, req.Msg.GetMfaToken(), code, recovery, metadataFromRequest(req))
	if err != nil {
		return nil, h.toMFAConnectError(err)
	}

	return connect.NewResponse(presenter.LoginResponse(result)), nil
}

// BeginMFAEnrollment issues a secret and provisioning URI for the caller.
func (h *AuthHandler) BeginMFAEnrollment(
	ctx context.Context,
	req *connect.Request[auth.BeginMFAEnrollmentRequest],
) (*connect.Response[auth.BeginMFAEnrollmentResponse], error) {
	claims, err := h.mfaClaims(ctx)
	if err != nil {
		return nil, err
	}

	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid user in token"))
	}

	// The email labels the entry in the authenticator app, so the user can tell
	// which account a code belongs to.
	accountName := claims.Email
	if accountName == "" {
		accountName = claims.Username
	}

	uri, secret, err := h.mfa.BeginEnrollment(ctx, userID, accountName)
	if err != nil {
		return nil, h.toMFAConnectError(err)
	}

	return connect.NewResponse(&auth.BeginMFAEnrollmentResponse{
		ProvisioningUri: uri,
		Secret:          secret,
	}), nil
}

// ConfirmMFAEnrollment activates MFA and returns the one-time recovery codes.
func (h *AuthHandler) ConfirmMFAEnrollment(
	ctx context.Context,
	req *connect.Request[auth.ConfirmMFAEnrollmentRequest],
) (*connect.Response[auth.ConfirmMFAEnrollmentResponse], error) {
	claims, err := h.mfaClaims(ctx)
	if err != nil {
		return nil, err
	}

	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid user in token"))
	}

	codes, err := h.mfa.ConfirmEnrollment(ctx, userID, strings.TrimSpace(req.Msg.GetCode()))
	if err != nil {
		return nil, h.toMFAConnectError(err)
	}

	return connect.NewResponse(&auth.ConfirmMFAEnrollmentResponse{
		RecoveryCodes: codes,
		Message:       "Two-factor authentication is on. Save these recovery codes — they are shown only once.",
	}), nil
}

// DisableMFA turns MFA off for the caller, after proving the current factor.
func (h *AuthHandler) DisableMFA(
	ctx context.Context,
	req *connect.Request[auth.DisableMFARequest],
) (*connect.Response[commonpb.Response], error) {
	claims, err := h.mfaClaims(ctx)
	if err != nil {
		return nil, err
	}

	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid user in token"))
	}

	code := strings.TrimSpace(req.Msg.GetCode())
	recovery := strings.TrimSpace(req.Msg.GetRecoveryCode())
	if code == "" && recovery == "" {
		// Requiring a factor is what stops a stolen session from stripping MFA.
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("a verification code or a recovery code is required to disable two-factor authentication"))
	}

	if err := h.mfa.Disable(ctx, userID, claims.Role, code, recovery); err != nil {
		return nil, h.toMFAConnectError(err)
	}

	msg := "Two-factor authentication is off."
	return connect.NewResponse(&commonpb.Response{
		Success: true,
		Message: &msg,
	}), nil
}

// RegenerateRecoveryCodes replaces the caller's recovery codes.
func (h *AuthHandler) RegenerateRecoveryCodes(
	ctx context.Context,
	req *connect.Request[auth.RegenerateRecoveryCodesRequest],
) (*connect.Response[auth.RegenerateRecoveryCodesResponse], error) {
	claims, err := h.mfaClaims(ctx)
	if err != nil {
		return nil, err
	}

	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid user in token"))
	}

	codes, err := h.mfa.RegenerateRecoveryCodes(ctx, userID, strings.TrimSpace(req.Msg.GetCode()))
	if err != nil {
		return nil, h.toMFAConnectError(err)
	}

	return connect.NewResponse(&auth.RegenerateRecoveryCodesResponse{
		RecoveryCodes: codes,
		Message:       "New recovery codes generated. The previous set no longer works.",
	}), nil
}

// GetMFAStatus reports the caller's MFA state for the Settings UI.
func (h *AuthHandler) GetMFAStatus(
	ctx context.Context,
	req *connect.Request[auth.GetMFAStatusRequest],
) (*connect.Response[auth.GetMFAStatusResponse], error) {
	claims, err := h.mfaClaims(ctx)
	if err != nil {
		return nil, err
	}

	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid user in token"))
	}

	status, err := h.mfa.Status(ctx, userID, claims.Role)
	if err != nil {
		return nil, h.toMFAConnectError(err)
	}

	resp := &auth.GetMFAStatusResponse{
		Enabled:                status.Enabled,
		RecoveryCodesRemaining: int32(status.RecoveryCodesRemaining),
		RequiredByPolicy:       status.RequiredByPolicy,
	}
	if status.EnrolledAt != nil {
		resp.EnrolledAt = timestamppb.New(*status.EnrolledAt)
	}
	return connect.NewResponse(resp), nil
}

// mfaClaims resolves the caller from the auth token. The user is never taken
// from the request body: that would let anyone enrol or disable MFA for any
// account by changing a field.
func (h *AuthHandler) mfaClaims(ctx context.Context) (*interceptors.Claims, error) {
	if h.mfa == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("MFA is not configured"))
	}

	claims, err := interceptors.GetClaimsFromContext(ctx)
	if err != nil || claims == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	return claims, nil
}

// toMFAConnectError maps MFA errors onto Connect codes.
//
// The messages stay deliberately uninformative about which part failed: telling
// a caller apart "wrong code" from "that code was already used" leaks whether
// they have a live secret.
func (h *AuthHandler) toMFAConnectError(err error) error {
	switch {
	case errors.Is(err, mfa.ErrLockedOut):
		return connect.NewError(connect.CodeResourceExhausted,
			errors.New("too many incorrect codes. Try again in 15 minutes"))

	case errors.Is(err, mfa.ErrInvalidCode), errors.Is(err, mfa.ErrCodeReplayed):
		return connect.NewError(connect.CodeUnauthenticated, errors.New("that code is not valid"))

	case errors.Is(err, mfa.ErrNotEnrolled), errors.Is(err, mfa.ErrNotFound):
		return connect.NewError(connect.CodeFailedPrecondition,
			errors.New("two-factor authentication is not set up for this account"))

	case errors.Is(err, mfa.ErrAlreadyEnrolled):
		return connect.NewError(connect.CodeFailedPrecondition,
			errors.New("two-factor authentication is already on. Turn it off before setting it up again"))

	case errors.Is(err, mfa.ErrNoEncryptionKey), errors.Is(err, mfa.ErrCorruptSecret):
		// A configuration fault, not the user's problem. Log it loudly and say
		// nothing useful to the caller.
		h.logger.Error("mfa secret could not be handled", slog.String("error", err.Error()))
		return connect.NewError(connect.CodeInternal, errors.New("two-factor authentication is unavailable"))

	case errors.Is(err, service.ErrAccountInactive):
		return connect.NewError(connect.CodePermissionDenied, errors.New("this account is inactive"))
	}

	return h.toConnectError(err)
}
