package mfa

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func testKey() []byte {
	return bytes.Repeat([]byte{0x2a}, KeyLength)
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	c, err := NewCipher(testKey())
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}

	secret, _ := GenerateSecret()
	sealed, err := c.Encrypt(secret)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// The stored bytes must not contain the secret — that is the entire point of
	// encrypting at rest.
	if strings.Contains(string(sealed), secret) {
		t.Error("the plaintext secret appears in the stored ciphertext")
	}

	got, err := c.Decrypt(sealed)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != secret {
		t.Errorf("Decrypt = %q, want %q", got, secret)
	}
}

// GCM nonce reuse under a single key is catastrophic, so two encryptions of the
// same secret must not produce the same bytes.
func TestEncryptUsesAFreshNoncePerCall(t *testing.T) {
	c, _ := NewCipher(testKey())

	a, err := c.Encrypt("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	b, err := c.Encrypt("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if bytes.Equal(a, b) {
		t.Error("encrypting the same secret twice produced identical ciphertext — nonce is being reused")
	}
}

// MFA with no key configured must fail loudly. Silently storing plaintext would
// turn a deployment mistake into an undetectable secret leak.
func TestNewCipherRejectsAMissingOrWrongSizedKey(t *testing.T) {
	if _, err := NewCipher(nil); !errors.Is(err, ErrNoEncryptionKey) {
		t.Errorf("nil key: expected ErrNoEncryptionKey, got %v", err)
	}
	if _, err := NewCipher([]byte("too-short")); err == nil {
		t.Error("a short key must be rejected, not padded")
	}
}

func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	c, _ := NewCipher(testKey())
	sealed, _ := c.Encrypt("JBSWY3DPEHPK3PXP")

	// Flip a bit in the ciphertext body. GCM authenticates, so this must be
	// detected rather than producing garbage that silently rejects every code.
	tampered := bytes.Clone(sealed)
	tampered[len(tampered)-1] ^= 0x01

	if _, err := c.Decrypt(tampered); !errors.Is(err, ErrCorruptSecret) {
		t.Errorf("expected ErrCorruptSecret for tampered input, got %v", err)
	}
}

func TestDecryptRejectsAWrongKey(t *testing.T) {
	c, _ := NewCipher(testKey())
	sealed, _ := c.Encrypt("JBSWY3DPEHPK3PXP")

	other, _ := NewCipher(bytes.Repeat([]byte{0x99}, KeyLength))
	if _, err := other.Decrypt(sealed); !errors.Is(err, ErrCorruptSecret) {
		t.Errorf("expected ErrCorruptSecret with a different key, got %v", err)
	}
}

func TestDecryptRejectsTruncatedInput(t *testing.T) {
	c, _ := NewCipher(testKey())

	if _, err := c.Decrypt([]byte{1, 2, 3}); !errors.Is(err, ErrCorruptSecret) {
		t.Errorf("expected ErrCorruptSecret for input shorter than a nonce, got %v", err)
	}
}
