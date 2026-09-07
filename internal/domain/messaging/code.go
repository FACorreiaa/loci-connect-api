// Package messaging links a chat platform to a Loci account, so somebody can
// ask for an itinerary from their phone and have it continue in the web app.
//
// Only the linking and routing half lives here. Inbound messages arrive from
// the platform through an adapter (see the telegram subpackage), and what
// answers them is an Answerer the caller supplies — which is how the same
// conversation the web app uses can serve a chat without this package knowing
// anything about itineraries.
package messaging

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"strings"
)

// codeAlphabet is what link codes are drawn from.
//
// Missing on purpose: 0 and O, 1 and I and L. The code is read off a screen and
// typed into a chat window by hand, and a character somebody cannot tell apart
// is a failed link they have no way to diagnose — the bot can only say the code
// was wrong. Losing a little entropy is cheap next to that; the expiry and the
// single use are what make a short code safe, not its alphabet.
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// codeLength is a compromise between typing it and guessing it.
//
// Eight characters from a 31-character alphabet is about 40 bits. That would be
// weak for a bearer token and is fine here: a code is valid for minutes, works
// once, and redemption is rate limited, so an attacker gets very few attempts
// at a target that stops existing.
const codeLength = 8

// NewCode returns a fresh link code.
func NewCode() (string, error) {
	buf := make([]byte, codeLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}

	out := make([]byte, codeLength)
	for i, b := range buf {
		// The alphabet length does not divide 256, so this is very slightly
		// biased. It does not matter at this scale — the security comes from
		// the expiry and the single use — and rejection sampling here would be
		// ceremony over a code that lives for minutes.
		out[i] = codeAlphabet[int(b)%len(codeAlphabet)]
	}
	return string(out), nil
}

// NormaliseCode puts a code into the form it is stored in: upper case, with
// the spaces and punctuation a chat client or a person may have added removed.
//
// It keeps only alphabet characters, so it must be given something that is
// already a code rather than a sentence containing one — "Code: ABCD2345"
// normalises to "CDEABCD2345", because C, D and E are letters codes are made
// of. FindCode is what handles a sentence.
func NormaliseCode(raw string) string {
	var sb strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(raw)) {
		if strings.ContainsRune(codeAlphabet, r) {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// FindCode returns the link code in a message, or "" if there is none.
//
// People do not only send the code. They send "Code: ABCD2345", or paste it
// with the hyphen it was displayed with, or let their keyboard lower-case it.
// Each word is tried on its own first, so prose around the code does not become
// part of it; the whole message is tried afterwards, which is what catches a
// code a chat client split with a space.
//
// A wrong guess costs nothing: it fails redemption and answers the same way
// every other bad code does.
func FindCode(text string) string {
	for _, field := range strings.Fields(text) {
		if candidate := NormaliseCode(field); len(candidate) == codeLength {
			return candidate
		}
	}
	if candidate := NormaliseCode(text); len(candidate) == codeLength {
		return candidate
	}
	return ""
}

// HashCode is what the database stores. The code itself never is, so a dump
// cannot be redeemed.
func HashCode(code string) []byte {
	sum := sha256.Sum256([]byte(NormaliseCode(code)))
	return sum[:]
}

// SameCode compares two hashes without leaking how far they matched.
func SameCode(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}
