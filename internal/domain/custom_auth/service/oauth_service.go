package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/gorilla/sessions"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/apple"
	"github.com/markbates/goth/providers/google"
)

// Apple's client secret is not a secret the operator stores — it is a JWT the
// server signs with the .p8 private key, and Apple rejects one whose lifetime
// exceeds six months (15777000 seconds).
//
// So the secret is generated here and regenerated on a timer. A static
// APPLE_SECRET, which is what this used to read, works until the JWT somebody
// minted by hand expires — and then Apple sign-in fails on a date nobody has in
// mind, with a message that says nothing about expiry.
//
// The lifetime asked for is well under the ceiling and the renewal interval is
// far shorter still: signing an ES256 token once a day costs nothing, and it
// means the secret is never close to expiring however long the process runs.
const (
	appleSecretLifetime = 150 * 24 * time.Hour
	appleSecretRenewal  = 24 * time.Hour
)

// OAuthConfig holds configuration for OAuth providers.
type OAuthConfig struct {
	GoogleClientID     string
	GoogleClientSecret string

	// Apple. ApplePrivateKey is the contents of the .p8 file downloaded from
	// the Apple developer portal, PEM and all — not a path to it, and not a
	// pre-signed JWT.
	AppleClientID   string
	AppleTeamID     string
	AppleKeyID      string
	ApplePrivateKey string

	CallbackBaseURL string
	SessionSecret   string
}

// appleFields returns the Apple settings by name, for reporting which of them
// are missing.
func (c OAuthConfig) appleFields() map[string]string {
	return map[string]string{
		"APPLE_CLIENT_ID":   c.AppleClientID,
		"APPLE_TEAM_ID":     c.AppleTeamID,
		"APPLE_KEY_ID":      c.AppleKeyID,
		"APPLE_PRIVATE_KEY": c.ApplePrivateKey,
	}
}

// missingAppleFields lists the empty Apple settings, sorted for a stable error.
func (c OAuthConfig) missingAppleFields() []string {
	var missing []string
	for name, value := range c.appleFields() {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	return missing
}

// OAuthService handles OAuth authentication flows.
type OAuthService struct {
	config OAuthConfig
	logger *slog.Logger
}

// NewOAuthService creates a new OAuth service and registers its providers.
//
// A provider with none of its settings present is simply off — that is how a
// developer runs without Google or Apple credentials. A provider with SOME of
// its settings present is an error: the operator meant it to work, and quietly
// skipping it leaves the button on the sign-in page failing with nothing in the
// logs to say why.
func NewOAuthService(config OAuthConfig, logger *slog.Logger) (*OAuthService, error) {
	if logger == nil {
		logger = slog.Default()
	}

	if config.SessionSecret != "" {
		store := sessions.NewCookieStore([]byte(config.SessionSecret))
		store.Options.HttpOnly = true
		store.Options.Secure = true
		store.Options.SameSite = 0 // Lax for OAuth redirects
		gothic.Store = store
	}

	s := &OAuthService{config: config, logger: logger}

	if config.GoogleClientID != "" && config.GoogleClientSecret != "" {
		goth.UseProviders(google.New(
			config.GoogleClientID,
			config.GoogleClientSecret,
			config.CallbackBaseURL+"/google/callback",
			"email", "profile",
		))
		logger.Info("oauth provider registered", slog.String("provider", "google"))
	}

	switch missing := config.missingAppleFields(); len(missing) {
	case len(config.appleFields()):
		// None of them set: Apple is off, deliberately.
	case 0:
		if err := s.registerApple(); err != nil {
			return nil, fmt.Errorf("apple sign-in: %w", err)
		}
		logger.Info("oauth provider registered", slog.String("provider", "apple"))
	default:
		return nil, fmt.Errorf(
			"apple sign-in is partly configured: %s unset. Set all four or none; see docs/oauth-setup.md",
			strings.Join(missing, ", "),
		)
	}

	return s, nil
}

// appleMakeSecret signs the client secret JWT Apple expects in place of a
// static secret.
//
// Split out from registerApple so it can be signed without also being handed to
// goth, which keeps it private and offers no way to read it back.
func appleMakeSecret(cfg OAuthConfig, now time.Time) (string, error) {
	secret, err := apple.MakeSecret(apple.SecretParams{
		PKCS8PrivateKey: cfg.ApplePrivateKey,
		TeamId:          cfg.AppleTeamID,
		KeyId:           cfg.AppleKeyID,
		ClientId:        cfg.AppleClientID,
		Iat:             int(now.Unix()),
		Exp:             int(now.Add(appleSecretLifetime).Unix()),
	})
	if err != nil {
		// Deliberately not wrapping err: the message from a bad PEM can echo
		// its contents, and this is a signing key.
		return "", errors.New("could not sign the client secret from APPLE_PRIVATE_KEY; " +
			"it must be the PKCS#8 contents of the .p8 file, PEM header included")
	}
	if secret == nil {
		return "", errors.New("apple.MakeSecret returned no secret")
	}
	return *secret, nil
}

// registerApple signs a fresh client secret and installs the Apple provider.
//
// goth holds the secret inside the provider it was built with, so renewing it
// means building a new provider. goth.UseProviders keys on the provider name,
// so this replaces the Apple entry and leaves Google alone.
func (s *OAuthService) registerApple() error {
	secret, err := appleMakeSecret(s.config, time.Now())
	if err != nil {
		return err
	}

	// The fourth argument is an *http.Client, NOT a secret generator — the
	// comment that used to sit here said otherwise, which is how a static
	// APPLE_SECRET survived this long.
	goth.UseProviders(apple.New(
		s.config.AppleClientID,
		secret,
		s.config.CallbackBaseURL+"/apple/callback",
		nil,
		apple.ScopeName,
		apple.ScopeEmail,
	))
	return nil
}

// RunAppleSecretRefresh re-signs the Apple client secret until ctx is cancelled.
//
// Returns nil immediately when Apple is not configured, so the caller can start
// it unconditionally. A failure to re-sign is logged rather than returned: the
// current secret is still valid for months, so one bad tick is not a reason to
// stop trying — or to take anything else down.
func (s *OAuthService) RunAppleSecretRefresh(ctx context.Context) error {
	if len(s.config.missingAppleFields()) != 0 {
		return nil
	}

	ticker := time.NewTicker(appleSecretRenewal)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := s.registerApple(); err != nil {
				s.logger.Error("could not renew the apple client secret",
					slog.String("error", err.Error()))
				continue
			}
			s.logger.Debug("apple client secret renewed",
				slog.Duration("lifetime", appleSecretLifetime))
		}
	}
}

// LoadOAuthConfigFromEnv loads OAuth configuration from environment variables.
func LoadOAuthConfigFromEnv() OAuthConfig {
	return OAuthConfig{
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		AppleClientID:      os.Getenv("APPLE_CLIENT_ID"),
		AppleTeamID:        os.Getenv("APPLE_TEAM_ID"),
		AppleKeyID:         os.Getenv("APPLE_KEY_ID"),
		ApplePrivateKey:    os.Getenv("APPLE_PRIVATE_KEY"),
		CallbackBaseURL:    os.Getenv("OAUTH_CALLBACK_URL"),
		SessionSecret:      os.Getenv("SESSION_SECRET"),
	}
}

// GetAuthURL generates the OAuth authorization URL for the specified provider
func (s *OAuthService) GetAuthURL(provider, redirectURI string) (authURL, state string, err error) {
	gothProvider, err := goth.GetProvider(provider)
	if err != nil {
		return "", "", fmt.Errorf("provider not configured: %w", err)
	}

	session, err := gothProvider.BeginAuth(redirectURI)
	if err != nil {
		return "", "", fmt.Errorf("failed to begin auth: %w", err)
	}

	authURL, err = session.GetAuthURL()
	if err != nil {
		return "", "", fmt.Errorf("failed to get auth URL: %w", err)
	}

	// Return URL and serialized session state for stateless verification
	return authURL, session.Marshal(), nil
}

// CompleteAuth exchanges the authorization code for user information
func (s *OAuthService) CompleteAuth(provider, code, sessionState string) (*goth.User, error) {
	gothProvider, err := goth.GetProvider(provider)
	if err != nil {
		return nil, fmt.Errorf("provider not configured: %w", err)
	}

	session, err := gothProvider.UnmarshalSession(sessionState)
	if err != nil {
		return nil, fmt.Errorf("invalid session state: %w", err)
	}

	_, err = session.Authorize(gothProvider, url.Values{"code": {code}})
	if err != nil {
		return nil, fmt.Errorf("failed to authorize: %w", err)
	}

	user, err := gothProvider.FetchUser(session)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user: %w", err)
	}

	return &user, nil
}

// IsProviderConfigured checks if a provider is configured
func (s *OAuthService) IsProviderConfigured(provider string) bool {
	_, err := goth.GetProvider(provider)
	return err == nil
}
