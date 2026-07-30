// Package mfa implements TOTP multi-factor auth: secret issuance, enrolment
// confirmation, code verification with replay and brute-force protection, and
// single-use recovery codes.
//
// The security-relevant decisions are deliberate and documented where they are
// made, because the failure modes here are quiet: a secret stored in plaintext,
// a code that can be replayed inside its window, or an unthrottled 6-digit
// guess all look exactly like working MFA until someone attacks them.
package mfa

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

// ErrNoEncryptionKey is returned when MFA is used without a configured key.
// Failing closed matters: falling back to plaintext storage would turn a
// misconfiguration into a silent, permanent secret leak.
var ErrNoEncryptionKey = errors.New("mfa: MFA_SECRET_KEY is not configured")

// ErrCorruptSecret means the stored ciphertext did not authenticate — a wrong
// key, or tampering.
var ErrCorruptSecret = errors.New("mfa: stored secret could not be decrypted")

// KeyLength is the required MFA_SECRET_KEY size. AES-256 leaves no reason to
// accept anything shorter.
const KeyLength = 32

// Cipher encrypts and decrypts TOTP secrets at rest.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher builds a cipher from a 32-byte key.
//
// AES-GCM rather than plain AES: it authenticates as well as encrypts, so a
// tampered-with row fails to decrypt instead of yielding a silently wrong secret
// that would reject every code the user's app generates.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) == 0 {
		return nil, ErrNoEncryptionKey
	}
	if len(key) != KeyLength {
		return nil, fmt.Errorf("mfa: MFA_SECRET_KEY must be %d bytes, got %d", KeyLength, len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("mfa: build cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("mfa: build GCM: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt seals a TOTP secret for storage.
//
// The nonce is random per call and prefixed to the ciphertext. Reusing a nonce
// under the same key is catastrophic for GCM, so it is never derived from
// anything caller-controlled such as the user id.
func (c *Cipher) Encrypt(secret string) ([]byte, error) {
	if secret == "" {
		return nil, errors.New("mfa: refusing to encrypt an empty secret")
	}

	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("mfa: generate nonce: %w", err)
	}

	// Seal appends to its first argument, so passing the nonce puts it in front
	// of the ciphertext where Decrypt expects it.
	return c.aead.Seal(nonce, nonce, []byte(secret), nil), nil
}

// Decrypt opens a stored secret.
func (c *Cipher) Decrypt(stored []byte) (string, error) {
	ns := c.aead.NonceSize()
	if len(stored) <= ns {
		return "", ErrCorruptSecret
	}

	nonce, ciphertext := stored[:ns], stored[ns:]
	plain, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		// Deliberately opaque: the caller has no use for the distinction between
		// a wrong key and tampering, and neither should reach a user.
		return "", ErrCorruptSecret
	}
	return string(plain), nil
}
