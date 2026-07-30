package mfa

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// The production cost is deliberately expensive; these tests exercise the code
// paths, not bcrypt's own work factor.
func TestMain(m *testing.M) {
	bcryptCost = bcrypt.MinCost
	m.Run()
}

// Guard the override above: if the default ever drifts below the password
// hashing cost, recovery codes become the weak link.
func TestProductionBcryptCostMatchesPasswordHashing(t *testing.T) {
	if got := productionBcryptCost; got < 12 {
		t.Errorf("production bcrypt cost = %d, want at least 12", got)
	}
}

func TestGenerateRecoveryCodesReturnsUniqueHashedCodes(t *testing.T) {
	plain, hashes, err := GenerateRecoveryCodes()
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}

	if len(plain) != RecoveryCodeCount || len(hashes) != RecoveryCodeCount {
		t.Fatalf("got %d codes / %d hashes, want %d of each", len(plain), len(hashes), RecoveryCodeCount)
	}

	seen := make(map[string]bool, len(plain))
	for _, code := range plain {
		if seen[code] {
			t.Errorf("duplicate recovery code %q", code)
		}
		seen[code] = true
	}

	// A hash that contains its own plaintext would defeat the point.
	for i, h := range hashes {
		if strings.Contains(h, NormalizeRecoveryCode(plain[i])) {
			t.Errorf("hash %d leaks its plaintext", i)
		}
		if !strings.HasPrefix(h, "$2a$") && !strings.HasPrefix(h, "$2b$") {
			t.Errorf("hash %d is not a bcrypt hash: %q", i, h)
		}
	}
}

// These are read off a screen and retyped, often days later.
func TestRecoveryCodesAvoidVisuallyAmbiguousCharacters(t *testing.T) {
	plain, _, err := GenerateRecoveryCodes()
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}

	for _, code := range plain {
		for _, bad := range []string{"0", "O", "1", "I", "L", "U", "V"} {
			if strings.Contains(code, bad) {
				t.Errorf("code %q contains the easily-misread character %q", code, bad)
			}
		}
		if want := recoveryGroups*recoveryGroupLen + recoveryGroups - 1; len(code) != want {
			t.Errorf("code %q has length %d, want %d", code, len(code), want)
		}
	}
}

func TestMatchRecoveryCodeFindsTheRightCode(t *testing.T) {
	plain, hashes, _ := GenerateRecoveryCodes()

	for i, code := range plain {
		if got := MatchRecoveryCode(code, hashes); got != i {
			t.Errorf("MatchRecoveryCode(%q) = %d, want %d", code, got, i)
		}
	}
}

func TestMatchRecoveryCodeRejectsUnknownCodes(t *testing.T) {
	_, hashes, _ := GenerateRecoveryCodes()

	for _, candidate := range []string{"", "ABCDE-FGHJK", "not a code"} {
		if got := MatchRecoveryCode(candidate, hashes); got != -1 {
			t.Errorf("MatchRecoveryCode(%q) = %d, want -1", candidate, got)
		}
	}
}

// People retype these from paper: lowercase, extra spaces, dashes dropped.
// Rejecting those would push users toward disabling MFA entirely.
func TestMatchRecoveryCodeToleratesHowPeopleActuallyTypeThem(t *testing.T) {
	plain, hashes, _ := GenerateRecoveryCodes()
	original := plain[3]

	variants := map[string]string{
		"lowercase":     strings.ToLower(original),
		"no dashes":     strings.ReplaceAll(original, "-", ""),
		"spaces":        strings.ReplaceAll(original, "-", " "),
		"leading space": "  " + original,
		"trailing":      original + "\n",
	}

	for label, v := range variants {
		if got := MatchRecoveryCode(v, hashes); got != 3 {
			t.Errorf("%s variant %q = %d, want 3", label, v, got)
		}
	}
}

func TestMatchRecoveryCodeHandlesAnEmptyHashList(t *testing.T) {
	if got := MatchRecoveryCode("ABCDE-FGHJK", nil); got != -1 {
		t.Errorf("got %d, want -1 when the user has no unused codes left", got)
	}
}
