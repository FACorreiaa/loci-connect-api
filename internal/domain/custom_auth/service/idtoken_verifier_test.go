package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/jwa"
	"github.com/lestrrat-go/jwx/jwk"
	"github.com/lestrrat-go/jwx/jwt"
)

const (
	testGoogleAud = "123-ios.apps.googleusercontent.com"
	testAppleAud  = "com.fernandocorreia.loci.beta"
	testNonce     = "0123456789abcdef-nonce"
)

// idTokenFixture is a throwaway signing key published as a JWKS, standing in
// for both providers' key endpoints.
type idTokenFixture struct {
	key      jwk.Key
	verifier *IDTokenVerifier
	now      time.Time
}

func newIDTokenFixture(t *testing.T) *idTokenFixture {
	t.Helper()
	raw, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	key, err := jwk.New(raw)
	if err != nil {
		t.Fatalf("jwk: %v", err)
	}
	_ = key.Set(jwk.KeyIDKey, "test-kid")
	_ = key.Set(jwk.AlgorithmKey, jwa.RS256)

	pub, err := key.PublicKey()
	if err != nil {
		t.Fatalf("public key: %v", err)
	}
	set := jwk.NewSet()
	set.Add(pub)
	body, err := json.Marshal(set)
	if err != nil {
		t.Fatalf("marshal jwks: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	v := newIDTokenVerifier(ctx, IDTokenConfig{
		GoogleAudiences: []string{testGoogleAud},
		AppleAudiences:  []string{"com.fernandocorreia.loci", testAppleAud},
	}, srv.URL+"/google", srv.URL+"/apple")

	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	v.now = func() time.Time { return now }
	return &idTokenFixture{key: key, verifier: v, now: now}
}

func (f *idTokenFixture) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	tok := jwt.New()
	for k, v := range claims {
		if err := tok.Set(k, v); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}
	signed, err := jwt.Sign(tok, jwa.RS256, f.key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return string(signed)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestIDTokenVerifier(t *testing.T) {
	f := newIDTokenFixture(t)

	google := func(over map[string]any) map[string]any {
		c := map[string]any{
			"iss":            "https://accounts.google.com",
			"aud":            testGoogleAud,
			"sub":            "google-sub-1",
			"email":          "person@example.com",
			"email_verified": true,
			"nonce":          testNonce,
			"exp":            f.now.Add(time.Hour).Unix(),
			"iat":            f.now.Unix(),
		}
		for k, v := range over {
			c[k] = v
		}
		return c
	}
	apple := func(over map[string]any) map[string]any {
		c := map[string]any{
			"iss":            "https://appleid.apple.com",
			"aud":            testAppleAud,
			"sub":            "001234.apple-sub.0001",
			"email":          "relay@privaterelay.appleid.com",
			"email_verified": "true",
			"nonce":          sha256Hex(testNonce),
			"exp":            f.now.Add(10 * time.Minute).Unix(),
			"iat":            f.now.Unix(),
		}
		for k, v := range over {
			c[k] = v
		}
		return c
	}

	tests := []struct {
		name      string
		provider  string
		claims    map[string]any
		nonce     string
		wantErr   error
		wantSub   string
		wantEmail string
	}{
		{
			name: "google valid", provider: "google", claims: google(nil), nonce: testNonce,
			wantSub: "google-sub-1", wantEmail: "person@example.com",
		},
		{
			name: "google issuer without scheme", provider: "google",
			claims: google(map[string]any{"iss": "accounts.google.com"}), nonce: testNonce, wantSub: "google-sub-1", wantEmail: "person@example.com",
		},
		{
			name: "apple valid with hashed nonce and string email_verified", provider: "apple", claims: apple(nil), nonce: testNonce,
			wantSub: "001234.apple-sub.0001", wantEmail: "relay@privaterelay.appleid.com",
		},
		{
			name: "unverified email is dropped", provider: "google",
			claims: google(map[string]any{"email_verified": false}), nonce: testNonce, wantSub: "google-sub-1",
		},
		{
			name: "wrong audience", provider: "google",
			claims: google(map[string]any{"aud": "someone-elses-app"}), nonce: testNonce, wantErr: ErrIDTokenInvalid,
		},
		{
			name: "wrong issuer", provider: "google",
			claims: google(map[string]any{"iss": "https://evil.example"}), nonce: testNonce, wantErr: ErrIDTokenInvalid,
		},
		{name: "apple token presented as google", provider: "google", claims: apple(nil), nonce: testNonce, wantErr: ErrIDTokenInvalid},
		{
			name: "expired", provider: "google",
			claims: google(map[string]any{"exp": f.now.Add(-5 * time.Minute).Unix()}), nonce: testNonce, wantErr: ErrIDTokenInvalid,
		},
		{
			name: "no expiry", provider: "google",
			claims: func() map[string]any { c := google(nil); delete(c, "exp"); return c }(), nonce: testNonce, wantErr: ErrIDTokenInvalid,
		},
		{name: "nonce mismatch", provider: "google", claims: google(nil), nonce: "a-different-nonce-entirely", wantErr: ErrIDTokenInvalid},
		{
			name: "apple raw nonce in token is refused", provider: "apple",
			claims: apple(map[string]any{"nonce": testNonce}), nonce: testNonce, wantErr: ErrIDTokenInvalid,
		},
		{
			name: "missing subject", provider: "apple",
			claims: func() map[string]any { c := apple(nil); delete(c, "sub"); return c }(), nonce: testNonce, wantErr: ErrIDTokenInvalid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.verifier.Verify(context.Background(), tc.provider, f.sign(t, tc.claims), tc.nonce)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if got.Subject != tc.wantSub || got.Email != tc.wantEmail {
				t.Fatalf("claims = %+v, want sub %q email %q", got, tc.wantSub, tc.wantEmail)
			}
		})
	}
}

func TestIDTokenVerifierRefusesForeignSignatures(t *testing.T) {
	f := newIDTokenFixture(t)
	other := newIDTokenFixture(t) // a different key, not in f's JWKS

	claims := map[string]any{
		"iss": "https://accounts.google.com", "aud": testGoogleAud, "sub": "x",
		"nonce": testNonce, "exp": f.now.Add(time.Hour).Unix(),
	}
	if _, err := f.verifier.Verify(context.Background(), "google", other.sign(t, claims), testNonce); !errors.Is(err, ErrIDTokenInvalid) {
		t.Fatalf("token signed by an unknown key: err = %v, want ErrIDTokenInvalid", err)
	}

	hs, err := jwt.Sign(jwt.New(), jwa.HS256, []byte("shared-secret-anyone-could-guess"))
	if err != nil {
		t.Fatalf("sign HS256: %v", err)
	}
	if _, err := f.verifier.Verify(context.Background(), "google", string(hs), testNonce); !errors.Is(err, ErrIDTokenInvalid) {
		t.Fatalf("HS256 token: err = %v, want ErrIDTokenInvalid", err)
	}
}

func TestIDTokenVerifierProviderOff(t *testing.T) {
	v := NewIDTokenVerifier(context.Background(), IDTokenConfig{GoogleAudiences: []string{testGoogleAud}})
	if v.IsConfigured("apple") {
		t.Fatal("apple should be off with no bundle IDs")
	}
	if _, err := v.Verify(context.Background(), "apple", "x.y.z", testNonce); !errors.Is(err, ErrIDTokenProviderNotConfigured) {
		t.Fatalf("err = %v, want ErrIDTokenProviderNotConfigured", err)
	}
}

func TestSplitList(t *testing.T) {
	got := splitList(" a, ,b ,")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("splitList = %q", got)
	}
}
