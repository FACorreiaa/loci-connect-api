// Package secret seals small values — bring-your-own-key provider credentials,
// external MCP access tokens — for storage in a database Loci does not treat
// as trusted.
//
// AES-256-GCM from the standard library. Not a homegrown construction and not
// a dependency: a sealed value is a []byte that goes into a bytea column and
// comes back the same, and the stdlib already has an authenticated cipher.
//
// # What this protects against, and what it does not
//
// It protects against a database dump: a stolen backup, a misconfigured
// replica, a support query that selected too much. The key lives in the
// process environment and never in Postgres, so possessing the rows is not
// possessing the values.
//
// It does not protect against a compromised application process, which holds
// the key by definition. One environment variable decrypts every stored value.
// That is the accepted trade for now; the upgrade is envelope encryption
// against a KMS, and the version byte in the wire format is the seam for it.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// version is the first byte of every sealed value.
//
// Not paranoia: it is what makes moving to a different cipher a decode-time
// branch rather than a data migration over a table full of secrets.
const version byte = 1

// KeySize is the required key length. AES-256 — the larger of the two sizes
// the stdlib offers, because there is no reason to choose the smaller.
const KeySize = 32

// nonceSize is GCM's standard nonce length. Anything else costs a slower path
// through the stdlib for no benefit.
const nonceSize = 12

// minSealedLen is version + key id + nonce + GCM tag. Anything shorter cannot
// be a sealed value, and checking it is what keeps a truncated column from
// becoming a slice-bounds panic.
const minSealedLen = 1 + 1 + nonceSize + 16

// ErrNotSealed means the value is not something this package produced, or has
// been altered since it was. Unknown key, wrong version, truncated blob, and
// failed authentication all report it: from the caller's side they are the
// same problem, which is that the value cannot be trusted.
var ErrNotSealed = errors.New("value is not sealed, or has been tampered with")

// Key is one encryption key and the id stamped into everything it seals.
type Key struct {
	// ID is written into the ciphertext so a value sealed under a retired key
	// can still be opened after rotation. Valid ids are 1-255; 0 is reserved
	// so that a zero-valued Key cannot pass for a configured one.
	ID uint8

	// Material must be exactly KeySize bytes.
	Material []byte
}

// Sealer seals with one active key and opens with any key it was given.
type Sealer struct {
	active uint8
	byID   map[uint8]cipher.AEAD
}

// NewSealer builds a sealer. The first key is the active one; the rest can
// only open.
//
// Rotation is therefore: put the new key first, redeploy, re-seal at leisure,
// then drop the old key once nothing is stamped with it.
func NewSealer(keys ...Key) (*Sealer, error) {
	if len(keys) == 0 {
		return nil, errors.New("secret: at least one key is required")
	}

	s := &Sealer{active: keys[0].ID, byID: make(map[uint8]cipher.AEAD, len(keys))}

	for i, key := range keys {
		if key.ID == 0 {
			return nil, fmt.Errorf("secret: key %d has id 0, which is reserved", i)
		}
		if len(key.Material) != KeySize {
			// The length is safe to name; the material is not.
			return nil, fmt.Errorf("secret: key %d is %d bytes, want %d", i, len(key.Material), KeySize)
		}
		if _, clash := s.byID[key.ID]; clash {
			return nil, fmt.Errorf("secret: key id %d appears twice", key.ID)
		}

		block, err := aes.NewCipher(key.Material)
		if err != nil {
			return nil, fmt.Errorf("secret: key %d is unusable", i)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("secret: key %d cannot be used with GCM", i)
		}

		s.byID[key.ID] = aead
	}

	return s, nil
}

// Seal encrypts plaintext, binding it to context.
//
// context is authenticated but not encrypted, and callers pass the identity of
// the row the value belongs to — a user id. It is a required argument rather
// than an option because the attack it stops is cheap and easy to miss:
// without it, a ciphertext copied from one row to another opens perfectly, and
// one person is quietly served by another person's key. Pass nil only when the
// value genuinely belongs to nothing.
//
// Wire format: version | key id | nonce (12) | ciphertext and tag.
func (s *Sealer) Seal(context, plaintext []byte) ([]byte, error) {
	aead, ok := s.byID[s.active]
	if !ok {
		return nil, errors.New("secret: the active key is missing")
	}

	out := make([]byte, 2+nonceSize, 2+nonceSize+len(plaintext)+aead.Overhead())
	out[0] = version
	out[1] = s.active

	nonce := out[2 : 2+nonceSize]
	if _, err := rand.Read(nonce); err != nil {
		return nil, errors.New("secret: cannot read random bytes")
	}

	// The header is authenticated along with the caller's context, so neither
	// the version nor the key id can be edited without the tag failing.
	return aead.Seal(out, nonce, plaintext, append(out[:2:2], context...)), nil
}

// Open reverses Seal.
//
// It fails for a value that was altered, one sealed under a key this process
// was not given, and one whose context does not match — every case reporting
// ErrNotSealed, because a caller cannot do anything different about them and a
// more specific message would only describe the secret.
func (s *Sealer) Open(context, sealed []byte) ([]byte, error) {
	if len(sealed) < minSealedLen || sealed[0] != version {
		return nil, ErrNotSealed
	}

	aead, ok := s.byID[sealed[1]]
	if !ok {
		return nil, ErrNotSealed
	}

	nonce := sealed[2 : 2+nonceSize]
	body := sealed[2+nonceSize:]

	plaintext, err := aead.Open(nil, nonce, body, append(sealed[:2:2], context...))
	if err != nil {
		// The underlying error is deliberately dropped. It says nothing useful
		// and wrapping it is how a ciphertext ends up formatted into a log.
		return nil, ErrNotSealed
	}
	return plaintext, nil
}

// ParseKeys reads the ENCRYPTION_KEY format: comma-separated entries, each
// either "<id>:<base64 of 32 bytes>" or bare base64 meaning id 1.
//
// The bare form exists so that `openssl rand -base64 32` pastes straight into
// a .env file. The explicit form is what rotation needs, and both are accepted
// everywhere so nobody has to migrate their configuration to rotate.
func ParseKeys(raw string) ([]Key, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	var keys []Key
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		id := 1
		encoded := entry
		if idPart, rest, found := strings.Cut(entry, ":"); found {
			parsed, err := strconv.Atoi(strings.TrimSpace(idPart))
			if err != nil {
				return nil, fmt.Errorf("key id %q is not a number", idPart)
			}
			id, encoded = parsed, strings.TrimSpace(rest)
		}

		if id < 1 || id > 255 {
			return nil, fmt.Errorf("key id %d is out of range; use 1-255", id)
		}

		material, err := decodeBase64(encoded)
		if err != nil {
			// The value is a key. Report the shape, never the content.
			return nil, fmt.Errorf("key %d is not valid base64", id)
		}
		if len(material) != KeySize {
			return nil, fmt.Errorf("key %d decodes to %d bytes, want %d", id, len(material), KeySize)
		}

		keys = append(keys, Key{ID: uint8(id), Material: material})
	}

	return keys, nil
}

// decodeBase64 accepts padded and unpadded input, because which one a
// generator produces is not something anyone should have to think about.
func decodeBase64(s string) ([]byte, error) {
	if strings.ContainsAny(s, "-_") {
		if strings.HasSuffix(s, "=") {
			return base64.URLEncoding.DecodeString(s)
		}
		return base64.RawURLEncoding.DecodeString(s)
	}
	if strings.HasSuffix(s, "=") {
		return base64.StdEncoding.DecodeString(s)
	}
	return base64.RawStdEncoding.DecodeString(s)
}
