package mfa

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeRepo is an in-memory Repository. The point of the interface: the login
// step-up rules are the security-critical part and they get tested without a
// database in the loop.
type fakeRepo struct {
	enrollment *Enrollment
	codes      []RecoveryCode
	getErr     error
	saveCalls  int
	replaced   [][]string
}

func (f *fakeRepo) Get(_ context.Context, _ uuid.UUID) (*Enrollment, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.enrollment == nil {
		return nil, ErrNotFound
	}
	return f.enrollment, nil
}

func (f *fakeRepo) UpsertSecret(_ context.Context, userID uuid.UUID, secret []byte) error {
	f.enrollment = &Enrollment{UserID: userID, SecretEncrypted: secret}
	return nil
}

func (f *fakeRepo) Confirm(_ context.Context, _ uuid.UUID, at time.Time) error {
	if f.enrollment == nil {
		return ErrNotFound
	}
	f.enrollment.ConfirmedAt = &at
	return nil
}

func (f *fakeRepo) Delete(_ context.Context, _ uuid.UUID) error {
	f.enrollment = nil
	f.codes = nil
	return nil
}

func (f *fakeRepo) SaveVerifyResult(_ context.Context, _ uuid.UUID, res VerifyResult) error {
	f.saveCalls++
	if f.enrollment != nil {
		if res.UsedStep > f.enrollment.LastUsedStep {
			f.enrollment.LastUsedStep = res.UsedStep
		}
		f.enrollment.FailedAttempts = res.FailedAttempts
		f.enrollment.LockedUntil = res.LockedUntil
	}
	return nil
}

func (f *fakeRepo) UnusedRecoveryCodes(_ context.Context, _ uuid.UUID) ([]RecoveryCode, error) {
	return f.codes, nil
}

func (f *fakeRepo) ReplaceRecoveryCodes(_ context.Context, _ uuid.UUID, hashes []string) error {
	f.replaced = append(f.replaced, hashes)
	f.codes = make([]RecoveryCode, len(hashes))
	for i, h := range hashes {
		f.codes[i] = RecoveryCode{ID: uuid.New(), CodeHash: h}
	}
	return nil
}

func (f *fakeRepo) MarkRecoveryCodeUsed(_ context.Context, id uuid.UUID, _ time.Time) (bool, error) {
	for i, c := range f.codes {
		if c.ID == id {
			f.codes = append(f.codes[:i], f.codes[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

type fixture struct {
	svc    *Service
	repo   *fakeRepo
	userID uuid.UUID
	now    time.Time
}

func newFixture(t *testing.T, policy Policy) *fixture {
	t.Helper()
	cipher, err := NewCipher(testKey())
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}

	f := &fixture{
		repo:   &fakeRepo{},
		userID: uuid.New(),
		now:    time.Unix(1_700_000_000, 0),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	f.svc = NewService(f.repo, cipher, policy, logger).WithClock(func() time.Time { return f.now })
	return f
}

// enrol runs a full begin+confirm cycle and returns the secret.
func (f *fixture) enrol(t *testing.T) string {
	t.Helper()
	_, secret, err := f.svc.BeginEnrollment(context.Background(), f.userID, "traveller@example.com")
	if err != nil {
		t.Fatalf("BeginEnrollment: %v", err)
	}
	if _, err := f.svc.ConfirmEnrollment(context.Background(), f.userID, codeAt(t, secret, f.now)); err != nil {
		t.Fatalf("ConfirmEnrollment: %v", err)
	}
	return secret
}

func TestBeginEnrollmentStoresTheSecretEncrypted(t *testing.T) {
	f := newFixture(t, Policy{})

	uri, secret, err := f.svc.BeginEnrollment(context.Background(), f.userID, "traveller@example.com")
	if err != nil {
		t.Fatalf("BeginEnrollment: %v", err)
	}
	if uri == "" || secret == "" {
		t.Fatal("expected both a provisioning URI and a secret")
	}

	stored := f.repo.enrollment
	if stored == nil {
		t.Fatal("no enrollment was stored")
	}
	if string(stored.SecretEncrypted) == secret {
		t.Error("the secret was stored in plaintext")
	}
}

// The failure mode this prevents: a user scans the QR, the scan silently fails,
// and one-step enrolment locks them out of their own account.
func TestBeginEnrollmentDoesNotActivateMFA(t *testing.T) {
	f := newFixture(t, Policy{})

	if _, _, err := f.svc.BeginEnrollment(context.Background(), f.userID, "a@b.com"); err != nil {
		t.Fatalf("BeginEnrollment: %v", err)
	}

	enabled, err := f.svc.IsEnabled(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("IsEnabled: %v", err)
	}
	if enabled {
		t.Error("MFA must not be active before the enrolment is confirmed")
	}
}

func TestConfirmEnrollmentActivatesMFAAndReturnsRecoveryCodes(t *testing.T) {
	f := newFixture(t, Policy{})

	_, secret, _ := f.svc.BeginEnrollment(context.Background(), f.userID, "a@b.com")
	codes, err := f.svc.ConfirmEnrollment(context.Background(), f.userID, codeAt(t, secret, f.now))
	if err != nil {
		t.Fatalf("ConfirmEnrollment: %v", err)
	}
	if len(codes) != RecoveryCodeCount {
		t.Errorf("got %d recovery codes, want %d", len(codes), RecoveryCodeCount)
	}

	enabled, _ := f.svc.IsEnabled(context.Background(), f.userID)
	if !enabled {
		t.Error("MFA should be active after confirmation")
	}
}

func TestConfirmEnrollmentRejectsAWrongCode(t *testing.T) {
	f := newFixture(t, Policy{})

	if _, _, err := f.svc.BeginEnrollment(context.Background(), f.userID, "a@b.com"); err != nil {
		t.Fatalf("BeginEnrollment: %v", err)
	}
	if _, err := f.svc.ConfirmEnrollment(context.Background(), f.userID, "000000"); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("expected ErrInvalidCode, got %v", err)
	}

	enabled, _ := f.svc.IsEnabled(context.Background(), f.userID)
	if enabled {
		t.Error("a failed confirmation must not activate MFA")
	}
}

// Re-enrolling without proving possession of the current factor would let a
// hijacked session swap the second factor for one the attacker controls.
func TestBeginEnrollmentRefusesWhenAlreadyEnrolled(t *testing.T) {
	f := newFixture(t, Policy{})
	f.enrol(t)

	if _, _, err := f.svc.BeginEnrollment(context.Background(), f.userID, "a@b.com"); !errors.Is(err, ErrAlreadyEnrolled) {
		t.Errorf("expected ErrAlreadyEnrolled, got %v", err)
	}
}

func TestVerifyForLoginAcceptsAValidTOTPCode(t *testing.T) {
	f := newFixture(t, Policy{})
	secret := f.enrol(t)

	// Move past the step consumed by the enrolment confirmation.
	f.now = f.now.Add(Period * time.Second)

	if err := f.svc.VerifyForLogin(context.Background(), f.userID, codeAt(t, secret, f.now), ""); err != nil {
		t.Errorf("VerifyForLogin: %v", err)
	}
}

// The confirmation code must not double as a login code inside its window.
func TestTheEnrollmentCodeCannotBeReplayedToLogIn(t *testing.T) {
	f := newFixture(t, Policy{})
	secret := f.enrol(t)

	used := codeAt(t, secret, f.now)
	err := f.svc.VerifyForLogin(context.Background(), f.userID, used, "")
	if !errors.Is(err, ErrCodeReplayed) {
		t.Errorf("expected ErrCodeReplayed, got %v", err)
	}
}

func TestVerifyForLoginRejectsAUserWhoIsNotEnrolled(t *testing.T) {
	f := newFixture(t, Policy{})

	if err := f.svc.VerifyForLogin(context.Background(), f.userID, "123456", ""); !errors.Is(err, ErrNotEnrolled) {
		t.Errorf("expected ErrNotEnrolled, got %v", err)
	}
}

// A pending enrolment is not protection, and must not be able to satisfy a
// challenge either.
func TestVerifyForLoginRejectsAnUnconfirmedEnrollment(t *testing.T) {
	f := newFixture(t, Policy{})
	_, secret, _ := f.svc.BeginEnrollment(context.Background(), f.userID, "a@b.com")

	err := f.svc.VerifyForLogin(context.Background(), f.userID, codeAt(t, secret, f.now), "")
	if !errors.Is(err, ErrNotEnrolled) {
		t.Errorf("expected ErrNotEnrolled, got %v", err)
	}
}

func TestVerifyForLoginAcceptsARecoveryCodeAndConsumesIt(t *testing.T) {
	f := newFixture(t, Policy{})
	_, secret, _ := f.svc.BeginEnrollment(context.Background(), f.userID, "a@b.com")
	codes, err := f.svc.ConfirmEnrollment(context.Background(), f.userID, codeAt(t, secret, f.now))
	if err != nil {
		t.Fatalf("ConfirmEnrollment: %v", err)
	}

	if err := f.svc.VerifyForLogin(context.Background(), f.userID, "", codes[0]); err != nil {
		t.Fatalf("VerifyForLogin with a recovery code: %v", err)
	}

	// Single-use is the whole contract: a reusable recovery code is a permanent
	// bypass for anyone who sees it once.
	if err := f.svc.VerifyForLogin(context.Background(), f.userID, "", codes[0]); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("a used recovery code must be rejected, got %v", err)
	}

	if got := len(f.repo.codes); got != RecoveryCodeCount-1 {
		t.Errorf("%d codes remain, want %d", got, RecoveryCodeCount-1)
	}
}

func TestRecoveryCodeFailuresCountTowardTheLockout(t *testing.T) {
	f := newFixture(t, Policy{})
	f.enrol(t)

	for range MaxFailedAttempts {
		if err := f.svc.VerifyForLogin(context.Background(), f.userID, "", "ABCDE-FGHJK"); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("expected ErrInvalidCode, got %v", err)
		}
	}

	if f.repo.enrollment.LockedUntil == nil {
		t.Fatal("repeated recovery-code guesses must trigger a lockout")
	}
	if err := f.svc.VerifyForLogin(context.Background(), f.userID, "", "ABCDE-FGHJK"); !errors.Is(err, ErrLockedOut) {
		t.Errorf("expected ErrLockedOut, got %v", err)
	}
}

// A lockout that only covered TOTP would leave recovery codes as the cheap way
// past it.
func TestALockoutFromTOTPAlsoBlocksRecoveryCodes(t *testing.T) {
	f := newFixture(t, Policy{})
	_, secret, _ := f.svc.BeginEnrollment(context.Background(), f.userID, "a@b.com")
	codes, _ := f.svc.ConfirmEnrollment(context.Background(), f.userID, codeAt(t, secret, f.now))

	for range MaxFailedAttempts {
		_ = f.svc.VerifyForLogin(context.Background(), f.userID, "000000", "")
	}

	if err := f.svc.VerifyForLogin(context.Background(), f.userID, "", codes[0]); !errors.Is(err, ErrLockedOut) {
		t.Errorf("expected ErrLockedOut for a recovery code during a lockout, got %v", err)
	}
}

func TestFailedAttemptsArePersistedSoTheCounterSurvivesRequests(t *testing.T) {
	f := newFixture(t, Policy{})
	f.enrol(t)
	before := f.repo.saveCalls

	_ = f.svc.VerifyForLogin(context.Background(), f.userID, "000000", "")

	if f.repo.saveCalls <= before {
		t.Error("a failed attempt must be persisted, or the rate limit is a no-op")
	}
	if f.repo.enrollment.FailedAttempts != 1 {
		t.Errorf("FailedAttempts = %d, want 1", f.repo.enrollment.FailedAttempts)
	}
}

func TestDisableRequiresAValidCode(t *testing.T) {
	f := newFixture(t, Policy{})
	f.enrol(t)
	f.now = f.now.Add(Period * time.Second)

	if err := f.svc.Disable(context.Background(), f.userID, "user", "000000", ""); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("expected ErrInvalidCode, got %v", err)
	}
	if enabled, _ := f.svc.IsEnabled(context.Background(), f.userID); !enabled {
		t.Error("a failed disable must leave MFA active")
	}
}

func TestDisableRemovesEnrollmentAndRecoveryCodes(t *testing.T) {
	f := newFixture(t, Policy{})
	secret := f.enrol(t)
	f.now = f.now.Add(Period * time.Second)

	if err := f.svc.Disable(context.Background(), f.userID, "user", codeAt(t, secret, f.now), ""); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if enabled, _ := f.svc.IsEnabled(context.Background(), f.userID); enabled {
		t.Error("MFA should be off after Disable")
	}
	if len(f.repo.codes) != 0 {
		t.Error("recovery codes must not survive a disable — a re-enrolment would inherit them")
	}
}

func TestDisableIsRefusedWhenPolicyRequiresMFAForTheRole(t *testing.T) {
	f := newFixture(t, ParsePolicy("admin, owner"))
	secret := f.enrol(t)
	f.now = f.now.Add(Period * time.Second)

	// Even with a perfectly valid code, an admin cannot opt out.
	err := f.svc.Disable(context.Background(), f.userID, "Admin", codeAt(t, secret, f.now), "")
	if err == nil {
		t.Fatal("expected policy to refuse the disable")
	}
	if enabled, _ := f.svc.IsEnabled(context.Background(), f.userID); !enabled {
		t.Error("MFA must remain active for a policy-required role")
	}

	// A role outside the policy is unaffected.
	if err := f.svc.Disable(context.Background(), f.userID, "user", codeAt(t, secret, f.now), ""); err != nil {
		t.Errorf("a non-required role should be able to disable: %v", err)
	}
}

// Someone holding one leaked recovery code must not be able to mint ten fresh
// ones and lock the real owner out.
func TestRegenerateRecoveryCodesRefusesARecoveryCode(t *testing.T) {
	f := newFixture(t, Policy{})
	_, secret, _ := f.svc.BeginEnrollment(context.Background(), f.userID, "a@b.com")
	codes, _ := f.svc.ConfirmEnrollment(context.Background(), f.userID, codeAt(t, secret, f.now))

	// Passing a valid recovery code where a TOTP code is expected must fail.
	if _, err := f.svc.RegenerateRecoveryCodes(context.Background(), f.userID, codes[0]); err == nil {
		t.Error("expected a recovery code to be rejected for regeneration")
	}
}

func TestRegenerateRecoveryCodesReplacesTheOldSet(t *testing.T) {
	f := newFixture(t, Policy{})
	_, secret, _ := f.svc.BeginEnrollment(context.Background(), f.userID, "a@b.com")
	old, _ := f.svc.ConfirmEnrollment(context.Background(), f.userID, codeAt(t, secret, f.now))
	f.now = f.now.Add(Period * time.Second)

	fresh, err := f.svc.RegenerateRecoveryCodes(context.Background(), f.userID, codeAt(t, secret, f.now))
	if err != nil {
		t.Fatalf("RegenerateRecoveryCodes: %v", err)
	}
	if len(fresh) != RecoveryCodeCount {
		t.Fatalf("got %d codes, want %d", len(fresh), RecoveryCodeCount)
	}

	// The old codes must be dead, or regeneration after a suspected leak is
	// pointless.
	if err := f.svc.VerifyForLogin(context.Background(), f.userID, "", old[0]); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("an old recovery code must stop working, got %v", err)
	}
	if err := f.svc.VerifyForLogin(context.Background(), f.userID, "", fresh[0]); err != nil {
		t.Errorf("a new recovery code should work, got %v", err)
	}
}

func TestStatusReportsEnrollmentAndRemainingCodes(t *testing.T) {
	f := newFixture(t, ParsePolicy("admin"))

	st, err := f.svc.Status(context.Background(), f.userID, "user")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Enabled {
		t.Error("a user with no enrollment must report disabled")
	}

	_, secret, _ := f.svc.BeginEnrollment(context.Background(), f.userID, "a@b.com")

	// A pending enrolment must still report disabled — telling the user they are
	// protected when they are not is the worst possible answer here.
	st, _ = f.svc.Status(context.Background(), f.userID, "user")
	if st.Enabled {
		t.Error("a pending enrollment must report disabled")
	}

	codes, _ := f.svc.ConfirmEnrollment(context.Background(), f.userID, codeAt(t, secret, f.now))
	_ = f.svc.VerifyForLogin(context.Background(), f.userID, "", codes[0])

	st, _ = f.svc.Status(context.Background(), f.userID, "admin")
	if !st.Enabled {
		t.Error("expected enabled after confirmation")
	}
	if st.RecoveryCodesRemaining != RecoveryCodeCount-1 {
		t.Errorf("RecoveryCodesRemaining = %d, want %d", st.RecoveryCodesRemaining, RecoveryCodeCount-1)
	}
	if st.EnrolledAt == nil {
		t.Error("expected an enrolment timestamp")
	}
	if !st.RequiredByPolicy {
		t.Error("expected RequiredByPolicy for the admin role")
	}
}

func TestParsePolicyIsCaseAndSpaceInsensitive(t *testing.T) {
	p := ParsePolicy(" Admin , OWNER ,, ")

	for _, role := range []string{"admin", "ADMIN", " Owner "} {
		if !p.Requires(role) {
			t.Errorf("Requires(%q) = false, want true", role)
		}
	}
	if p.Requires("user") {
		t.Error("Requires(\"user\") = true, want false")
	}
	if ParsePolicy("").Requires("admin") {
		t.Error("an empty policy must not require MFA for anyone")
	}
}
