package secret_test

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/FACorreiaa/loci-connect-api/pkg/secret"
)

func key(t *testing.T, id uint8) secret.Key {
	t.Helper()

	material := make([]byte, secret.KeySize)
	if _, err := rand.Read(material); err != nil {
		t.Fatalf("random key: %v", err)
	}
	return secret.Key{ID: id, Material: material}
}

func newSealer(t *testing.T, keys ...secret.Key) *secret.Sealer {
	t.Helper()

	s, err := secret.NewSealer(keys...)
	if err != nil {
		t.Fatalf("new sealer: %v", err)
	}
	return s
}

func TestSealOpenRoundTrip(t *testing.T) {
	s := newSealer(t, key(t, 1))
	ctx := []byte("a user id")

	cases := map[string][]byte{
		"empty":           {},
		"one byte":        []byte("x"),
		"a real key":      []byte("sk-or-v1-" + strings.Repeat("a1b2c3d4", 8)),
		"eight kilobytes": bytes.Repeat([]byte("k"), 8<<10),
	}

	for name, plaintext := range cases {
		t.Run(name, func(t *testing.T) {
			sealed, err := s.Seal(ctx, plaintext)
			if err != nil {
				t.Fatalf("seal: %v", err)
			}
			opened, err := s.Open(ctx, sealed)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			if !bytes.Equal(opened, plaintext) {
				t.Fatal("the opened value differs from the sealed one")
			}
		})
	}
}

// Cheap, and the only test that catches an implementation which quietly
// stopped encrypting — a bug nobody would notice in production.
func TestSealedValueDoesNotContainThePlaintext(t *testing.T) {
	s := newSealer(t, key(t, 1))
	plaintext := []byte("sk-or-v1-do-not-store-me-in-the-clear")

	sealed, err := s.Seal([]byte("user"), plaintext)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Contains(sealed, plaintext) {
		t.Fatal("the sealed value contains the plaintext")
	}
}

func TestSealIsNonDeterministic(t *testing.T) {
	s := newSealer(t, key(t, 1))
	ctx, plaintext := []byte("user"), []byte("same input")

	first, err := s.Seal(ctx, plaintext)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	second, err := s.Seal(ctx, plaintext)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("two seals of the same input are identical; the nonce is not random")
	}
}

// Every byte, including the version and key-id header — an implementation that
// trusted those without authenticating them would pass a laxer test.
func TestOpenRejectsAnyAlteredByte(t *testing.T) {
	s := newSealer(t, key(t, 1))
	ctx := []byte("user")

	sealed, err := s.Seal(ctx, []byte("sk-or-v1-secret"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	for i := range sealed {
		tampered := bytes.Clone(sealed)
		tampered[i] ^= 0x01

		if _, err := s.Open(ctx, tampered); err == nil {
			t.Fatalf("byte %d could be flipped and the value still opened", i)
		}
	}
}

// A short bytea column must be an error, never a slice-bounds panic.
func TestOpenRejectsTruncatedInputWithoutPanicking(t *testing.T) {
	s := newSealer(t, key(t, 1))
	ctx := []byte("user")

	sealed, err := s.Seal(ctx, []byte("secret"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	for n := 0; n < len(sealed); n++ {
		if _, err := s.Open(ctx, sealed[:n]); err == nil {
			t.Fatalf("a %d-byte value opened successfully", n)
		}
	}
	if _, err := s.Open(ctx, nil); err == nil {
		t.Fatal("nil opened successfully")
	}
}

// The row-swap test: this is what the context argument exists for.
func TestOpenRejectsAValueSealedForAnotherRow(t *testing.T) {
	s := newSealer(t, key(t, 1))

	sealed, err := s.Seal([]byte("user-a"), []byte("user a's api key"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	if _, err := s.Open([]byte("user-b"), sealed); err == nil {
		t.Fatal("user A's ciphertext opened under user B; a copied row would serve the wrong key")
	}
	if _, err := s.Open(nil, sealed); err == nil {
		t.Fatal("dropping the context opened the value")
	}
}

func TestOpenRejectsAnUnknownKeyID(t *testing.T) {
	sealed, err := newSealer(t, key(t, 2)).Seal([]byte("user"), []byte("secret"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	if _, err := newSealer(t, key(t, 1)).Open([]byte("user"), sealed); err == nil {
		t.Fatal("a value stamped with key 2 opened under a sealer holding only key 1")
	}
}

func TestOpenRejectsAnUnknownVersion(t *testing.T) {
	s := newSealer(t, key(t, 1))
	ctx := []byte("user")

	sealed, err := s.Seal(ctx, []byte("secret"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	sealed[0] = 0xFF

	if _, err := s.Open(ctx, sealed); err == nil {
		t.Fatal("a value with an unknown version opened")
	}
}

func TestRotation(t *testing.T) {
	old, current := key(t, 1), key(t, 2)
	ctx := []byte("user")

	sealedUnderOld, err := newSealer(t, old).Seal(ctx, []byte("secret"))
	if err != nil {
		t.Fatalf("seal under the old key: %v", err)
	}

	// During rotation both keys are configured, newest first.
	rotating := newSealer(t, current, old)

	opened, err := rotating.Open(ctx, sealedUnderOld)
	if err != nil {
		t.Fatalf("the old key's value did not open during rotation: %v", err)
	}
	if string(opened) != "secret" {
		t.Fatal("the rotated sealer returned the wrong plaintext")
	}

	fresh, err := rotating.Seal(ctx, []byte("secret"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if fresh[1] != current.ID {
		t.Fatalf("new values are stamped with key %d, want the active %d", fresh[1], current.ID)
	}

	// And once the old key is dropped, its values are gone — which is the risk
	// rotation has to be finished before taking.
	if _, err := newSealer(t, current).Open(ctx, sealedUnderOld); err == nil {
		t.Fatal("a value stamped with a dropped key still opened")
	}
}

func TestNewSealerRejectsBadKeys(t *testing.T) {
	good := key(t, 1)

	cases := map[string][]secret.Key{
		"no keys":      {},
		"id zero":      {{ID: 0, Material: good.Material}},
		"too short":    {{ID: 1, Material: good.Material[:16]}},
		"too long":     {{ID: 1, Material: append(bytes.Clone(good.Material), 'x')}},
		"duplicate":    {good, {ID: 1, Material: key(t, 1).Material}},
		"nil material": {{ID: 1}},
	}

	for name, keys := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := secret.NewSealer(keys...); err == nil {
				t.Fatal("the sealer was built from an invalid key set")
			}
		})
	}
}

// A future `fmt.Errorf("...%q", sealed)` would be a slow leak into the logs.
func TestErrorsRevealNothing(t *testing.T) {
	s := newSealer(t, key(t, 1))
	plaintext := []byte("sk-or-v1-this-must-never-be-logged")

	sealed, err := s.Seal([]byte("user-a"), plaintext)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	tampered := bytes.Clone(sealed)
	tampered[len(tampered)-1] ^= 0xFF

	for name, attempt := range map[string][]byte{
		"tampered":    tampered,
		"truncated":   sealed[:8],
		"wrong key":   sealed,
		"wrong shape": []byte("not sealed at all"),
	} {
		t.Run(name, func(t *testing.T) {
			var err error
			if name == "wrong key" {
				_, err = s.Open([]byte("user-b"), attempt)
			} else {
				_, err = s.Open([]byte("user-a"), attempt)
			}
			if err == nil {
				t.Fatal("the value opened")
			}
			msg := err.Error()
			if strings.Contains(msg, string(plaintext)) {
				t.Fatalf("the error names the plaintext: %q", msg)
			}
			if strings.Contains(msg, base64.StdEncoding.EncodeToString(sealed)) {
				t.Fatalf("the error names the ciphertext: %q", msg)
			}
		})
	}
}

func TestParseKeys(t *testing.T) {
	material := make([]byte, secret.KeySize)
	if _, err := rand.Read(material); err != nil {
		t.Fatalf("random: %v", err)
	}
	padded := base64.StdEncoding.EncodeToString(material)
	unpadded := base64.RawStdEncoding.EncodeToString(material)
	urlSafe := base64.RawURLEncoding.EncodeToString(material)

	t.Run("accepted", func(t *testing.T) {
		cases := map[string]struct {
			raw     string
			count   int
			firstID uint8
		}{
			"bare padded":   {padded, 1, 1},
			"bare unpadded": {unpadded, 1, 1},
			"url safe":      {urlSafe, 1, 1},
			"explicit id":   {"7:" + padded, 1, 7},
			"two keys":      {"2:" + padded + ",1:" + unpadded, 2, 2},
			"whitespace":    {" 2: " + padded + " , 1:" + unpadded + " ", 2, 2},
		}

		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				keys, err := secret.ParseKeys(tc.raw)
				if err != nil {
					t.Fatalf("parse: %v", err)
				}
				if len(keys) != tc.count {
					t.Fatalf("got %d keys, want %d", len(keys), tc.count)
				}
				if keys[0].ID != tc.firstID {
					t.Fatalf("first key id = %d, want %d", keys[0].ID, tc.firstID)
				}
				if len(keys[0].Material) != secret.KeySize {
					t.Fatalf("material is %d bytes, want %d", len(keys[0].Material), secret.KeySize)
				}
			})
		}
	})

	t.Run("empty is not an error", func(t *testing.T) {
		keys, err := secret.ParseKeys("   ")
		if err != nil || keys != nil {
			t.Fatalf("keys = %v, err = %v; an unset variable means encryption is off, not misconfigured", keys, err)
		}
	})

	t.Run("rejected", func(t *testing.T) {
		short := base64.StdEncoding.EncodeToString(material[:16])

		for name, raw := range map[string]string{
			"not base64":      "!!!!not base64!!!!",
			"too short":       short,
			"id zero":         "0:" + padded,
			"id too high":     "256:" + padded,
			"id not a number": "abc:" + padded,
		} {
			t.Run(name, func(t *testing.T) {
				keys, err := secret.ParseKeys(raw)
				if err == nil {
					t.Fatalf("parsed %d keys from invalid input", len(keys))
				}
				if strings.Contains(err.Error(), padded) || strings.Contains(err.Error(), string(material)) {
					t.Fatalf("the error names the key material: %q", err)
				}
			})
		}
	})
}
