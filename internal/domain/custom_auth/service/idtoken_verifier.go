package service

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/lestrrat-go/jwx/jwa"
	"github.com/lestrrat-go/jwx/jwk"
	"github.com/lestrrat-go/jwx/jws"
	"github.com/lestrrat-go/jwx/jwt"
)

// Native sign-in (the iOS Apple sheet and the Google SDK) ends with an ID token
// on the device rather than a code for the server to exchange. The token is
// only as good as the checks below: it is signed by the provider, but it is
// also handed to us by the client, so the audience has to be OUR app and the
// nonce has to be the one this attempt generated, or a token minted for some
// other app — or replayed from an earlier sign-in — would be accepted.
//
// The subject is what the account is keyed on, and it matches the web flow:
// goth's Google and Apple providers resolve to the same `sub`, so a person who
// signs in natively lands on the account they already have from the website.

const (
	googleJWKSURL = "https://www.googleapis.com/oauth2/v3/certs"
	appleJWKSURL  = "https://appleid.apple.com/auth/keys"

	// Both providers publish new keys well before signing with them, so an
	// hourly refresh never meets a token signed by a key it has not seen.
	jwksRefreshInterval = time.Hour
	idTokenClockSkew    = time.Minute
)

var (
	// ErrIDTokenProviderNotConfigured means no audience is configured for the
	// provider, so there is nothing a token could legitimately be issued to.
	ErrIDTokenProviderNotConfigured = errors.New("native sign-in is not configured for this provider")
	// ErrIDTokenInvalid covers every way a token can fail verification. The
	// reason is wrapped for the log, never shown to the caller.
	ErrIDTokenInvalid = errors.New("invalid ID token")
)

// IDTokenClaims is what a verified ID token says about the person.
type IDTokenClaims struct {
	Subject string
	// Email is empty unless the provider marked it verified. The sign-in path
	// links an unknown subject to an existing account by email, so an
	// unverified address must never reach it.
	Email string
}

// idTokenIssuer is one provider's verification rules.
type idTokenIssuer struct {
	jwksURL   string
	issuers   []string
	audiences []string
	// Apple puts the SHA-256 hex digest of the nonce in the token; Google puts
	// the nonce itself.
	hashedNonce bool
}

// IDTokenVerifier verifies native sign-in ID tokens for Google and Apple.
type IDTokenVerifier struct {
	keys      *jwk.AutoRefresh
	providers map[string]idTokenIssuer
	now       func() time.Time
}

// IDTokenConfig lists the audiences each provider's tokens may be issued to.
// A provider with no audiences is off.
type IDTokenConfig struct {
	// GoogleAudiences are the iOS OAuth client IDs. Not the web client: the
	// Google SDK issues tokens to the client it signed in with.
	GoogleAudiences []string
	// AppleAudiences are the app bundle IDs. The native sheet issues tokens to
	// the bundle, not to the web Services ID.
	AppleAudiences []string
}

// LoadIDTokenConfigFromEnv reads GOOGLE_IOS_CLIENT_IDS and APPLE_BUNDLE_IDS,
// each a comma-separated list.
func LoadIDTokenConfigFromEnv() IDTokenConfig {
	return IDTokenConfig{
		GoogleAudiences: splitList(os.Getenv("GOOGLE_IOS_CLIENT_IDS")),
		AppleAudiences:  splitList(os.Getenv("APPLE_BUNDLE_IDS")),
	}
}

func splitList(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// NewIDTokenVerifier builds a verifier against the providers' published keys.
// ctx bounds the background key refresh.
func NewIDTokenVerifier(ctx context.Context, cfg IDTokenConfig) *IDTokenVerifier {
	return newIDTokenVerifier(ctx, cfg, googleJWKSURL, appleJWKSURL)
}

func newIDTokenVerifier(ctx context.Context, cfg IDTokenConfig, googleKeys, appleKeys string) *IDTokenVerifier {
	v := &IDTokenVerifier{
		keys:      jwk.NewAutoRefresh(ctx),
		providers: map[string]idTokenIssuer{},
		now:       time.Now,
	}
	if len(cfg.GoogleAudiences) > 0 {
		v.providers["google"] = idTokenIssuer{
			jwksURL:   googleKeys,
			issuers:   []string{"https://accounts.google.com", "accounts.google.com"},
			audiences: cfg.GoogleAudiences,
		}
	}
	if len(cfg.AppleAudiences) > 0 {
		v.providers["apple"] = idTokenIssuer{
			jwksURL:     appleKeys,
			issuers:     []string{"https://appleid.apple.com"},
			audiences:   cfg.AppleAudiences,
			hashedNonce: true,
		}
	}
	for _, p := range v.providers {
		v.keys.Configure(p.jwksURL, jwk.WithRefreshInterval(jwksRefreshInterval))
	}
	return v
}

// IsConfigured reports whether native sign-in is on for provider.
func (v *IDTokenVerifier) IsConfigured(provider string) bool {
	_, ok := v.providers[provider]
	return ok
}

// Verify checks rawToken was issued by provider, to this app, for the attempt
// that generated rawNonce, and has not expired.
func (v *IDTokenVerifier) Verify(ctx context.Context, provider, rawToken, rawNonce string) (*IDTokenClaims, error) {
	p, ok := v.providers[provider]
	if !ok {
		return nil, ErrIDTokenProviderNotConfigured
	}

	// Pin the algorithm before trusting anything in the token. Both providers
	// sign with RS256; anything else is not theirs.
	msg, err := jws.Parse([]byte(rawToken))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIDTokenInvalid, err)
	}
	sigs := msg.Signatures()
	if len(sigs) != 1 || sigs[0].ProtectedHeaders().Algorithm() != jwa.RS256 {
		return nil, fmt.Errorf("%w: not a single RS256 signature", ErrIDTokenInvalid)
	}

	keys, err := v.keys.Fetch(ctx, p.jwksURL)
	if err != nil {
		return nil, fmt.Errorf("fetch %s signing keys: %w", provider, err)
	}

	tok, err := jwt.Parse([]byte(rawToken),
		jwt.WithKeySet(keys),
		jwt.WithValidate(true),
		jwt.WithAcceptableSkew(idTokenClockSkew),
		jwt.WithClock(jwt.ClockFunc(v.now)),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIDTokenInvalid, err)
	}

	if tok.Expiration().IsZero() {
		return nil, fmt.Errorf("%w: no expiry", ErrIDTokenInvalid)
	}
	if !slices.Contains(p.issuers, tok.Issuer()) {
		return nil, fmt.Errorf("%w: issuer %q", ErrIDTokenInvalid, tok.Issuer())
	}
	if !slices.ContainsFunc(tok.Audience(), func(aud string) bool { return slices.Contains(p.audiences, aud) }) {
		return nil, fmt.Errorf("%w: audience %v", ErrIDTokenInvalid, tok.Audience())
	}
	if tok.Subject() == "" {
		return nil, fmt.Errorf("%w: no subject", ErrIDTokenInvalid)
	}

	want := rawNonce
	if p.hashedNonce {
		sum := sha256.Sum256([]byte(rawNonce))
		want = hex.EncodeToString(sum[:])
	}
	got, _ := claimString(tok, "nonce")
	if rawNonce == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return nil, fmt.Errorf("%w: nonce mismatch", ErrIDTokenInvalid)
	}

	claims := &IDTokenClaims{Subject: tok.Subject()}
	if email, ok := claimString(tok, "email"); ok && claimTrue(tok, "email_verified") {
		claims.Email = email
	}
	return claims, nil
}

func claimString(tok jwt.Token, name string) (string, bool) {
	v, ok := tok.Get(name)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok && s != ""
}

// claimTrue reads a boolean claim. Apple has sent email_verified both as a
// JSON boolean and as the string "true".
func claimTrue(tok jwt.Token, name string) bool {
	v, ok := tok.Get(name)
	if !ok {
		return false
	}
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return b == "true"
	}
	return false
}
