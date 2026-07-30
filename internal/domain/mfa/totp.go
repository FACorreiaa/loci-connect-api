package mfa

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const (
	// Issuer is the label authenticator apps show next to the account.
	Issuer = "Loci"

	// Period is the TOTP time-step, in seconds. 30 is what every authenticator
	// app assumes by default; changing it would silently break enrolments.
	Period = 30

	// Digits per code.
	Digits = otp.DigitsSix

	// Skew is the number of time-steps either side of now that are accepted, to
	// tolerate clock drift between the phone and the server. 1 gives a ~90s
	// acceptance window, the standard trade-off: larger windows multiply the
	// number of codes valid at any instant.
	Skew = 1

	// MaxFailedAttempts before verification is locked out. A 6-digit code has a
	// 1-in-a-million chance per guess, so an unthrottled endpoint is brute-forceable
	// in hours; 5 attempts keeps it out of reach while tolerating typos.
	MaxFailedAttempts = 5

	// LockoutWindow is how long a locked-out user must wait.
	LockoutWindow = 15 * time.Minute

	// SecretBytes of entropy in a generated secret. RFC 4226 requires at least
	// 128 bits and recommends 160.
	SecretBytes = 20

	// RecoveryCodeCount issued per generation.
	RecoveryCodeCount = 10
)

var (
	// ErrInvalidCode means the submitted code did not match.
	ErrInvalidCode = errors.New("mfa: invalid verification code")

	// ErrCodeReplayed means the code was valid but has already been used.
	ErrCodeReplayed = errors.New("mfa: verification code already used")

	// ErrLockedOut means too many failed attempts.
	ErrLockedOut = errors.New("mfa: too many failed attempts, try again later")

	// ErrNotEnrolled means the user has no confirmed MFA.
	ErrNotEnrolled = errors.New("mfa: user is not enrolled in MFA")

	// ErrAlreadyEnrolled means MFA is already confirmed and must be disabled first.
	ErrAlreadyEnrolled = errors.New("mfa: user is already enrolled in MFA")
)

// GenerateSecret returns a new base32 TOTP secret.
func GenerateSecret() (string, error) {
	buf := make([]byte, SecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mfa: generate secret: %w", err)
	}
	// Authenticator apps expect unpadded base32 — the '=' padding is not part of
	// the otpauth key format and some apps reject it.
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), nil
}

// ProvisioningURI builds the otpauth:// URI that becomes the enrolment QR code.
//
// accountName should identify the account to a human scanning it — an email is
// the convention, so the entry is recognisable among the user's other codes.
func ProvisioningURI(secret, accountName string) string {
	key, err := otp.NewKeyFromURL(buildURL(secret, accountName))
	if err != nil {
		// buildURL is fully under our control; a parse failure would be a bug here,
		// not bad input, so return the raw URL rather than swallow it.
		return buildURL(secret, accountName)
	}
	return key.URL()
}

func buildURL(secret, accountName string) string {
	v := strings.NewReplacer(" ", "%20").Replace(accountName)
	return fmt.Sprintf(
		"otpauth://totp/%s:%s?secret=%s&issuer=%s&algorithm=SHA1&digits=6&period=%d",
		Issuer, v, secret, Issuer, Period,
	)
}

// VerifyState is the persisted per-user verification state that Verify needs to
// enforce replay and lockout rules.
type VerifyState struct {
	// LastUsedStep is the time-step of the last accepted code, or 0 if none.
	LastUsedStep int64
	// FailedAttempts since the last success.
	FailedAttempts int
	// LockedUntil is when the current lockout expires, if any.
	LockedUntil *time.Time
}

// VerifyResult is the outcome to persist after a verification attempt.
type VerifyResult struct {
	// UsedStep is the accepted time-step, to store as LastUsedStep.
	UsedStep int64
	// FailedAttempts is the new counter value (0 on success).
	FailedAttempts int
	// LockedUntil is the new lockout, if this attempt triggered one.
	LockedUntil *time.Time
}

// Verify checks a submitted TOTP code against the secret.
//
// It is a pure function of (secret, code, state, now) so the whole policy —
// skew, replay, lockout — is testable without a database or a real clock. The
// caller persists the returned VerifyResult; that split is what lets the rules
// be unit-tested and the storage be trivial.
func Verify(secret, code string, state VerifyState, now time.Time) (VerifyResult, error) {
	if state.LockedUntil != nil && now.Before(*state.LockedUntil) {
		return VerifyResult{
			FailedAttempts: state.FailedAttempts,
			LockedUntil:    state.LockedUntil,
		}, ErrLockedOut
	}

	// A lockout that has expired resets the counter — otherwise a user who once
	// hit the limit would be locked out again on their very next typo.
	attempts := state.FailedAttempts
	if state.LockedUntil != nil && !now.Before(*state.LockedUntil) {
		attempts = 0
	}

	code = strings.TrimSpace(strings.ReplaceAll(code, " ", ""))

	step, ok := matchStep(secret, code, now)
	if !ok {
		return failure(attempts, now), ErrInvalidCode
	}

	// The code is cryptographically valid, but a code stays valid for its whole
	// window. Accepting it twice would let anyone who observed it — over the
	// user's shoulder, in a log, in a phished form — reuse it before it expires.
	if step <= state.LastUsedStep {
		return failure(attempts, now), ErrCodeReplayed
	}

	return VerifyResult{UsedStep: step, FailedAttempts: 0}, nil
}

// matchStep finds which time-step within the skew window produced the code.
//
// pquerna's totp.Validate answers only yes/no, and the step is what replay
// protection needs, so the window is walked here.
func matchStep(secret, code string, now time.Time) (int64, bool) {
	opts := totp.ValidateOpts{
		Period:    Period,
		Skew:      0, // each candidate step is checked explicitly
		Digits:    Digits,
		Algorithm: otp.AlgorithmSHA1,
	}

	current := now.Unix() / Period
	for offset := -int64(Skew); offset <= int64(Skew); offset++ {
		step := current + offset
		at := time.Unix(step*Period, 0)
		valid, err := totp.ValidateCustom(code, secret, at, opts)
		if err == nil && valid {
			return step, true
		}
	}
	return 0, false
}

func failure(attempts int, now time.Time) VerifyResult {
	attempts++
	res := VerifyResult{FailedAttempts: attempts}
	if attempts >= MaxFailedAttempts {
		until := now.Add(LockoutWindow)
		res.LockedUntil = &until
	}
	return res
}

// ConstantTimeEqual compares two secrets without leaking their contents through
// timing. Used where a secret or token is compared outside bcrypt.
func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
