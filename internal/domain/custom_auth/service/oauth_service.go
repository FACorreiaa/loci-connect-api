package service

import (
	"fmt"
	"net/url"
	"os"

	"github.com/gorilla/sessions"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/apple"
	"github.com/markbates/goth/providers/google"
)

// OAuthConfig holds configuration for OAuth providers
type OAuthConfig struct {
	GoogleClientID     string
	GoogleClientSecret string
	AppleClientID      string
	AppleSecret        string
	CallbackBaseURL    string
	SessionSecret      string
}

// OAuthService handles OAuth authentication flows
type OAuthService struct {
	config OAuthConfig
}

// NewOAuthService creates a new OAuth service and initializes providers
func NewOAuthService(config OAuthConfig) *OAuthService {
	// Initialize session store for gothic
	if config.SessionSecret != "" {
		store := sessions.NewCookieStore([]byte(config.SessionSecret))
		store.Options.HttpOnly = true
		store.Options.Secure = true
		store.Options.SameSite = 0 // Lax for OAuth redirects
		gothic.Store = store
	}

	// Initialize providers
	providers := []goth.Provider{}

	if config.GoogleClientID != "" && config.GoogleClientSecret != "" {
		providers = append(providers, google.New(
			config.GoogleClientID,
			config.GoogleClientSecret,
			config.CallbackBaseURL+"/google/callback",
			"email", "profile",
		))
	}

	if config.AppleClientID != "" && config.AppleSecret != "" {
		providers = append(providers, apple.New(
			config.AppleClientID,
			config.AppleSecret,
			config.CallbackBaseURL+"/apple/callback",
			nil, // Use default secret generator
			apple.ScopeName,
			apple.ScopeEmail,
		))
	}

	if len(providers) > 0 {
		goth.UseProviders(providers...)
	}

	return &OAuthService{config: config}
}

// LoadOAuthConfigFromEnv loads OAuth configuration from environment variables
func LoadOAuthConfigFromEnv() OAuthConfig {
	return OAuthConfig{
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		AppleClientID:      os.Getenv("APPLE_CLIENT_ID"),
		AppleSecret:        os.Getenv("APPLE_SECRET"),
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
