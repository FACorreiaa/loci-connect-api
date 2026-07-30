package mfa

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Clock is injected so the lockout and challenge-expiry logic is testable.
type Clock func() time.Time

// Policy decides whether a role must have MFA. Populated from
// MFA_REQUIRED_FOR_ROLE.
type Policy struct {
	// RequiredRoles are roles for which MFA cannot be disabled once enrolled and
	// which the UI should push to enrol.
	RequiredRoles map[string]bool
}

// Requires reports whether the role must keep MFA.
func (p Policy) Requires(role string) bool {
	if len(p.RequiredRoles) == 0 {
		return false
	}
	return p.RequiredRoles[strings.ToLower(strings.TrimSpace(role))]
}

// ParsePolicy reads a comma-separated role list, e.g. "admin,owner".
func ParsePolicy(raw string) Policy {
	p := Policy{RequiredRoles: map[string]bool{}}
	for _, part := range strings.Split(raw, ",") {
		if role := strings.ToLower(strings.TrimSpace(part)); role != "" {
			p.RequiredRoles[role] = true
		}
	}
	return p
}

// Service holds the MFA use cases. It owns the secret cipher and the storage
// port, and nothing else: token issuance stays in the auth service, so there is
// exactly one place that can mint an access token.
type Service struct {
	repo   Repository
	cipher *Cipher
	policy Policy
	now    Clock
	logger *slog.Logger
}

// NewService builds the MFA service.
func NewService(repo Repository, cipher *Cipher, policy Policy, logger *slog.Logger) *Service {
	return &Service{
		repo:   repo,
		cipher: cipher,
		policy: policy,
		now:    time.Now,
		logger: logger,
	}
}

// WithClock overrides the clock, for tests.
func (s *Service) WithClock(c Clock) *Service {
	s.now = c
	return s
}

// Policy exposes the configured policy for status reporting.
func (s *Service) Policy() Policy { return s.policy }

// Status is what Settings needs to render the MFA section.
type Status struct {
	Enabled                bool
	RecoveryCodesRemaining int
	EnrolledAt             *time.Time
	RequiredByPolicy       bool
}

// Status reports the user's MFA state.
func (s *Service) Status(ctx context.Context, userID uuid.UUID, role string) (Status, error) {
	st := Status{RequiredByPolicy: s.policy.Requires(role)}

	enrollment, err := s.repo.Get(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if !enrollment.Confirmed() {
		// A pending enrolment is not protection; reporting it as enabled would tell
		// the user they are covered when they are not.
		return st, nil
	}

	codes, err := s.repo.UnusedRecoveryCodes(ctx, userID)
	if err != nil {
		return st, err
	}

	st.Enabled = true
	st.EnrolledAt = enrollment.ConfirmedAt
	st.RecoveryCodesRemaining = len(codes)
	return st, nil
}

// IsEnabled reports whether login must challenge this user.
func (s *Service) IsEnabled(ctx context.Context, userID uuid.UUID) (bool, error) {
	enrollment, err := s.repo.Get(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return enrollment.Confirmed(), nil
}

// BeginEnrollment issues a new secret and returns it with its provisioning URI.
//
// The secret is stored unconfirmed. Nothing about login changes until
// ConfirmEnrollment proves the user's app works — enrolling in one step would
// lock out anyone whose QR scan silently failed.
func (s *Service) BeginEnrollment(ctx context.Context, userID uuid.UUID, accountName string) (uri, secret string, err error) {
	enrollment, err := s.repo.Get(ctx, userID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return "", "", err
	}
	if enrollment.Confirmed() {
		// Re-enrolling would replace a working second factor without proving
		// possession of the current one. Disable first, which requires a code.
		return "", "", ErrAlreadyEnrolled
	}

	secret, err = GenerateSecret()
	if err != nil {
		return "", "", err
	}

	sealed, err := s.cipher.Encrypt(secret)
	if err != nil {
		return "", "", err
	}
	if err := s.repo.UpsertSecret(ctx, userID, sealed); err != nil {
		return "", "", err
	}

	return ProvisioningURI(secret, accountName), secret, nil
}

// ConfirmEnrollment activates MFA and returns the one-time recovery codes.
func (s *Service) ConfirmEnrollment(ctx context.Context, userID uuid.UUID, code string) ([]string, error) {
	enrollment, err := s.repo.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	if enrollment.Confirmed() {
		return nil, ErrAlreadyEnrolled
	}

	if err := s.verifyTOTP(ctx, enrollment, code); err != nil {
		return nil, err
	}

	plain, hashes, err := GenerateRecoveryCodes()
	if err != nil {
		return nil, err
	}
	if err := s.repo.ReplaceRecoveryCodes(ctx, userID, hashes); err != nil {
		return nil, err
	}

	// Confirm last. If recovery-code storage failed, the user would otherwise be
	// enrolled with no way back in from a lost phone.
	if err := s.repo.Confirm(ctx, userID, s.now()); err != nil {
		return nil, err
	}

	s.logger.InfoContext(ctx, "mfa enrolled", slog.String("user_id", userID.String()))
	return plain, nil
}

// VerifyForLogin checks a second factor during login step-up.
//
// Accepts either a TOTP code or a recovery code; the caller passes whichever the
// user submitted. It returns no tokens — issuing them stays with the auth
// service, so this cannot accidentally become a second token-minting path.
func (s *Service) VerifyForLogin(ctx context.Context, userID uuid.UUID, code, recoveryCode string) error {
	enrollment, err := s.repo.Get(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrNotEnrolled
		}
		return err
	}
	if !enrollment.Confirmed() {
		return ErrNotEnrolled
	}

	if strings.TrimSpace(recoveryCode) != "" {
		return s.consumeRecoveryCode(ctx, enrollment, recoveryCode)
	}
	return s.verifyTOTP(ctx, enrollment, code)
}

// VerifyCurrentFactor checks a code from an already-authenticated user, for the
// operations that change MFA itself.
func (s *Service) VerifyCurrentFactor(ctx context.Context, userID uuid.UUID, code, recoveryCode string) error {
	return s.VerifyForLogin(ctx, userID, code, recoveryCode)
}

// Disable turns MFA off after proving possession of the current factor.
func (s *Service) Disable(ctx context.Context, userID uuid.UUID, role, code, recoveryCode string) error {
	// Policy wins over the user's preference: an admin cannot opt out of the
	// requirement that applies to their role.
	if s.policy.Requires(role) {
		return fmt.Errorf("mfa: MFA is required for role %q and cannot be disabled", role)
	}

	if err := s.VerifyCurrentFactor(ctx, userID, code, recoveryCode); err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, userID); err != nil {
		return err
	}

	s.logger.WarnContext(ctx, "mfa disabled", slog.String("user_id", userID.String()))
	return nil
}

// RegenerateRecoveryCodes replaces every existing code with a fresh set.
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, userID uuid.UUID, code string) ([]string, error) {
	// A recovery code is deliberately not accepted here: someone holding a single
	// leaked code could otherwise mint ten fresh ones and take over the account.
	if err := s.VerifyCurrentFactor(ctx, userID, code, ""); err != nil {
		return nil, err
	}

	plain, hashes, err := GenerateRecoveryCodes()
	if err != nil {
		return nil, err
	}
	if err := s.repo.ReplaceRecoveryCodes(ctx, userID, hashes); err != nil {
		return nil, err
	}

	s.logger.InfoContext(ctx, "mfa recovery codes regenerated", slog.String("user_id", userID.String()))
	return plain, nil
}

func (s *Service) verifyTOTP(ctx context.Context, e *Enrollment, code string) error {
	secret, err := s.cipher.Decrypt(e.SecretEncrypted)
	if err != nil {
		return err
	}

	res, verifyErr := Verify(secret, code, VerifyState{
		LastUsedStep:   e.LastUsedStep,
		FailedAttempts: e.FailedAttempts,
		LockedUntil:    e.LockedUntil,
	}, s.now())

	// Persist the outcome either way. Skipping this on failure would make the
	// attempt counter useless, and skipping it on success would leave the replay
	// window open.
	if saveErr := s.repo.SaveVerifyResult(ctx, e.UserID, res); saveErr != nil {
		if verifyErr != nil {
			return verifyErr
		}
		return saveErr
	}
	return verifyErr
}

func (s *Service) consumeRecoveryCode(ctx context.Context, e *Enrollment, candidate string) error {
	// Recovery codes share the TOTP lockout: they are guessable in principle and
	// an unthrottled recovery endpoint would be the cheapest way past MFA.
	now := s.now()
	if e.LockedUntil != nil && now.Before(*e.LockedUntil) {
		return ErrLockedOut
	}

	codes, err := s.repo.UnusedRecoveryCodes(ctx, e.UserID)
	if err != nil {
		return err
	}

	hashes := make([]string, len(codes))
	for i, c := range codes {
		hashes[i] = c.CodeHash
	}

	idx := MatchRecoveryCode(candidate, hashes)
	if idx < 0 {
		attempts := e.FailedAttempts
		if e.LockedUntil != nil && !now.Before(*e.LockedUntil) {
			attempts = 0
		}
		if saveErr := s.repo.SaveVerifyResult(ctx, e.UserID, failure(attempts, now)); saveErr != nil {
			s.logger.ErrorContext(ctx, "mfa: could not record failed recovery attempt",
				slog.String("error", saveErr.Error()))
		}
		return ErrInvalidCode
	}

	consumed, err := s.repo.MarkRecoveryCodeUsed(ctx, codes[idx].ID, now)
	if err != nil {
		return err
	}
	if !consumed {
		// Lost the race with a concurrent submission of the same code.
		return ErrInvalidCode
	}

	if err := s.repo.SaveVerifyResult(ctx, e.UserID, VerifyResult{FailedAttempts: 0}); err != nil {
		return err
	}

	remaining := len(codes) - 1
	s.logger.WarnContext(ctx, "mfa recovery code used",
		slog.String("user_id", e.UserID.String()),
		slog.Int("remaining", remaining))
	return nil
}
