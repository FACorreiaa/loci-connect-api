package mfa

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// codeAt generates the code a real authenticator app would show at t.
func codeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateCodeCustom(secret, at, totp.ValidateOpts{
		Period:    Period,
		Digits:    Digits,
		Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	return code
}

func TestGenerateSecretIsUsableByAnAuthenticatorApp(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}

	// Padding in the secret breaks some authenticator apps, so assert its absence
	// rather than trusting the encoder's default.
	if strings.Contains(secret, "=") {
		t.Errorf("secret must be unpadded base32, got %q", secret)
	}

	now := time.Now()
	res, err := Verify(secret, codeAt(t, secret, now), VerifyState{}, now)
	if err != nil {
		t.Fatalf("a freshly generated secret must accept its own code: %v", err)
	}
	if res.UsedStep == 0 {
		t.Error("expected the accepted time-step to be reported")
	}
}

func TestVerifyAcceptsTheCurrentCode(t *testing.T) {
	secret, _ := GenerateSecret()
	now := time.Unix(1_700_000_000, 0)

	res, err := Verify(secret, codeAt(t, secret, now), VerifyState{}, now)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.FailedAttempts != 0 {
		t.Errorf("a success must reset the failure counter, got %d", res.FailedAttempts)
	}
	if want := now.Unix() / Period; res.UsedStep != want {
		t.Errorf("UsedStep = %d, want %d", res.UsedStep, want)
	}
}

// Phone clocks drift. Without a skew window, a user whose phone is 20 seconds
// fast can never sign in, and the failure looks like a wrong code.
func TestVerifyToleratesClockDriftWithinTheSkewWindow(t *testing.T) {
	secret, _ := GenerateSecret()
	now := time.Unix(1_700_000_000, 0)

	for _, drift := range []time.Duration{-Period * time.Second, Period * time.Second} {
		phoneTime := now.Add(drift)
		if _, err := Verify(secret, codeAt(t, secret, phoneTime), VerifyState{}, now); err != nil {
			t.Errorf("drift %v should be accepted, got %v", drift, err)
		}
	}
}

func TestVerifyRejectsCodesOutsideTheSkewWindow(t *testing.T) {
	secret, _ := GenerateSecret()
	now := time.Unix(1_700_000_000, 0)

	stale := codeAt(t, secret, now.Add(-5*Period*time.Second))
	if _, err := Verify(secret, stale, VerifyState{}, now); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("expected ErrInvalidCode for a long-expired code, got %v", err)
	}
}

// The whole point of tracking last_used_step: a code stays valid for up to 90
// seconds, so anyone who observes it could otherwise replay it.
func TestVerifyRefusesToReuseAnAlreadyUsedCode(t *testing.T) {
	secret, _ := GenerateSecret()
	now := time.Unix(1_700_000_000, 0)
	code := codeAt(t, secret, now)

	first, err := Verify(secret, code, VerifyState{}, now)
	if err != nil {
		t.Fatalf("first use must succeed: %v", err)
	}

	// Same code, one second later, with the state the caller would have persisted.
	_, err = Verify(secret, code, VerifyState{LastUsedStep: first.UsedStep}, now.Add(time.Second))
	if !errors.Is(err, ErrCodeReplayed) {
		t.Errorf("expected ErrCodeReplayed, got %v", err)
	}
}

// A replayed code must also count against the attempt limit, otherwise replay
// attempts are free.
func TestReplayCountsAsAFailedAttempt(t *testing.T) {
	secret, _ := GenerateSecret()
	now := time.Unix(1_700_000_000, 0)
	code := codeAt(t, secret, now)
	step := now.Unix() / Period

	res, _ := Verify(secret, code, VerifyState{LastUsedStep: step, FailedAttempts: 2}, now)
	if res.FailedAttempts != 3 {
		t.Errorf("FailedAttempts = %d, want 3", res.FailedAttempts)
	}
}

// An earlier step must be rejected too: without the `<=` comparison, an attacker
// who captured a code from the previous window could still use it.
func TestVerifyRejectsAnEarlierStepThanTheLastUsed(t *testing.T) {
	secret, _ := GenerateSecret()
	now := time.Unix(1_700_000_000, 0)
	previous := codeAt(t, secret, now.Add(-Period*time.Second))

	state := VerifyState{LastUsedStep: now.Unix() / Period}
	if _, err := Verify(secret, previous, state, now); !errors.Is(err, ErrCodeReplayed) {
		t.Errorf("expected ErrCodeReplayed for an older step, got %v", err)
	}
}

func TestVerifyLocksOutAfterMaxFailedAttempts(t *testing.T) {
	secret, _ := GenerateSecret()
	now := time.Unix(1_700_000_000, 0)

	state := VerifyState{FailedAttempts: MaxFailedAttempts - 1}
	res, err := Verify(secret, "000000", state, now)
	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("expected ErrInvalidCode, got %v", err)
	}
	if res.LockedUntil == nil {
		t.Fatal("hitting the attempt limit must set a lockout")
	}
	if got, want := res.LockedUntil.Sub(now), LockoutWindow; got != want {
		t.Errorf("lockout window = %v, want %v", got, want)
	}
}

// While locked out, even the correct code is refused — otherwise the lockout
// would not slow an attacker who is about to guess right.
func TestVerifyRefusesEvenAValidCodeWhileLockedOut(t *testing.T) {
	secret, _ := GenerateSecret()
	now := time.Unix(1_700_000_000, 0)
	until := now.Add(5 * time.Minute)

	state := VerifyState{FailedAttempts: MaxFailedAttempts, LockedUntil: &until}
	if _, err := Verify(secret, codeAt(t, secret, now), state, now); !errors.Is(err, ErrLockedOut) {
		t.Errorf("expected ErrLockedOut, got %v", err)
	}
}

// A user who once hit the limit must not be permanently one typo away from
// another lockout.
func TestExpiredLockoutResetsTheAttemptCounter(t *testing.T) {
	secret, _ := GenerateSecret()
	now := time.Unix(1_700_000_000, 0)
	expired := now.Add(-time.Minute)

	state := VerifyState{FailedAttempts: MaxFailedAttempts, LockedUntil: &expired}

	// A valid code after the lockout expires must simply work.
	if _, err := Verify(secret, codeAt(t, secret, now), state, now); err != nil {
		t.Fatalf("expected the lockout to have expired, got %v", err)
	}

	// And a wrong code must start counting from 1, not from the old 5.
	res, _ := Verify(secret, "000000", state, now)
	if res.FailedAttempts != 1 {
		t.Errorf("FailedAttempts = %d, want 1 after an expired lockout", res.FailedAttempts)
	}
	if res.LockedUntil != nil {
		t.Error("a single failure after an expired lockout must not re-lock")
	}
}

// Authenticator apps display codes as "123 456"; people paste them that way.
func TestVerifyIgnoresSpacingInSubmittedCodes(t *testing.T) {
	secret, _ := GenerateSecret()
	now := time.Unix(1_700_000_000, 0)
	code := codeAt(t, secret, now)
	spaced := code[:3] + " " + code[3:]

	if _, err := Verify(secret, spaced, VerifyState{}, now); err != nil {
		t.Errorf("a space-separated code must be accepted, got %v", err)
	}
}

func TestProvisioningURIContainsWhatAuthenticatorAppsNeed(t *testing.T) {
	secret, _ := GenerateSecret()
	uri := ProvisioningURI(secret, "traveller@example.com")

	key, err := otp.NewKeyFromURL(uri)
	if err != nil {
		t.Fatalf("the provisioning URI must parse as an otpauth key: %v", err)
	}
	if key.Secret() != secret {
		t.Errorf("secret = %q, want %q", key.Secret(), secret)
	}
	if key.Issuer() != Issuer {
		t.Errorf("issuer = %q, want %q", key.Issuer(), Issuer)
	}
	if !strings.Contains(key.AccountName(), "traveller@example.com") {
		t.Errorf("account name = %q, want it to identify the user", key.AccountName())
	}
	if got := key.Period(); got != Period {
		t.Errorf("period = %d, want %d", got, Period)
	}
}
