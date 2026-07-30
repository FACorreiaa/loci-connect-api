package mfa

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost matches the password hashing cost used in auth/service/password.go.
// Recovery codes are password-equivalent — each one is a full second factor — so
// they get password-equivalent treatment.
//
// A var, not a const, only so tests can lower it: hashing 10 codes at cost 12
// takes seconds, and a slow unit suite is a suite that stops being run. Nothing
// outside _test.go may change it.
const productionBcryptCost = 12

var bcryptCost = productionBcryptCost

// recoveryAlphabet excludes the characters people misread when copying a code
// off a screen: 0/O, 1/I/L, and the ambiguous U/V pair.
const recoveryAlphabet = "ABCDEFGHJKMNPQRSTWXYZ23456789"

const (
	// recoveryGroupLen is the length of each dash-separated group.
	recoveryGroupLen = 5
	// recoveryGroups per code. 2 groups of 5 over a 29-character alphabet is
	// ~48 bits — far beyond guessable, and still short enough to be typed.
	recoveryGroups = 2
)

// GenerateRecoveryCodes returns RecoveryCodeCount plaintext codes together with
// their bcrypt hashes.
//
// The plaintext is returned to be shown to the user exactly once and then
// discarded; only the hashes are persisted. This is the same reasoning as
// passwords: a database dump must not yield usable second factors.
func GenerateRecoveryCodes() (plain []string, hashes []string, err error) {
	plain = make([]string, 0, RecoveryCodeCount)
	hashes = make([]string, 0, RecoveryCodeCount)

	for range RecoveryCodeCount {
		code, err := newRecoveryCode()
		if err != nil {
			return nil, nil, err
		}
		hash, err := HashRecoveryCode(code)
		if err != nil {
			return nil, nil, err
		}
		plain = append(plain, code)
		hashes = append(hashes, hash)
	}
	return plain, hashes, nil
}

func newRecoveryCode() (string, error) {
	groups := make([]string, 0, recoveryGroups)
	max := big.NewInt(int64(len(recoveryAlphabet)))

	for range recoveryGroups {
		var sb strings.Builder
		for range recoveryGroupLen {
			// crypto/rand, not math/rand: these are credentials, and a predictable
			// sequence would let one leaked code expose the rest.
			n, err := rand.Int(rand.Reader, max)
			if err != nil {
				return "", fmt.Errorf("mfa: generate recovery code: %w", err)
			}
			sb.WriteByte(recoveryAlphabet[n.Int64()])
		}
		groups = append(groups, sb.String())
	}
	return strings.Join(groups, "-"), nil
}

// HashRecoveryCode hashes a single code for storage.
func HashRecoveryCode(code string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(NormalizeRecoveryCode(code)), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("mfa: hash recovery code: %w", err)
	}
	return string(h), nil
}

// NormalizeRecoveryCode makes user input comparable to what was hashed: people
// retype these from paper, with stray spaces, lowercase, or missing dashes.
func NormalizeRecoveryCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, " ", "")
	code = strings.ReplaceAll(code, "-", "")
	return code
}

// MatchRecoveryCode reports whether candidate matches the stored hash.
//
// Returns the index of the matching hash so the caller can mark that specific
// code used; -1 when nothing matches.
//
// Every hash is checked even after a match is found. bcrypt at cost 12 is slow
// by design, so short-circuiting would make the response time reveal how many
// unused codes the account has left.
func MatchRecoveryCode(candidate string, hashes []string) int {
	normalized := []byte(NormalizeRecoveryCode(candidate))
	match := -1
	for i, h := range hashes {
		if bcrypt.CompareHashAndPassword([]byte(h), normalized) == nil && match == -1 {
			match = i
		}
	}
	return match
}
