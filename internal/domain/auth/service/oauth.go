package service

import (
	"os"

	"github.com/gorilla/sessions"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/apple"
	"github.com/markbates/goth/providers/google"
)

// OAuthConfig holds OAuth configuration
type OAuthConfig struct {
	GoogleClientID     string
	GoogleClientSecret string
	AppleClientID      string
	AppleSecret        string
	AppleTeamID        string
	AppleKeyID         string
	CallbackURL        string
	SessionSecret      string
}

// InitOAuth initializes OAuth providers (Google and Apple only)
func InitOAuth(config OAuthConfig) error {
	// Set up session store for gothic
	store := sessions.NewCookieStore([]byte(config.SessionSecret))
	store.Options.HttpOnly = true
	store.Options.Secure = true // Set to true in production with HTTPS
	gothic.Store = store

	// Initialize providers
	providers := []goth.Provider{}

	// Google OAuth
	if config.GoogleClientID != "" && config.GoogleClientSecret != "" {
		providers = append(providers,
			google.New(
				config.GoogleClientID,
				config.GoogleClientSecret,
				config.CallbackURL+"/google/callback",
				"email", "profile",
			),
		)
	}

	// Apple OAuth
	if config.AppleClientID != "" && config.AppleSecret != "" {
		// Apple requires additional configuration
		// The secret is typically a JWT signed with your private key
		providers = append(providers,
			apple.New(
				config.AppleClientID,
				config.AppleSecret,
				config.CallbackURL+"/apple/callback",
				nil, // Optional client secret (use nil if using JWT)
				apple.ScopeName,
				apple.ScopeEmail,
			),
		)
	}

	goth.UseProviders(providers...)

	return nil
}

// LoadOAuthConfigFromEnv loads OAuth config from environment variables
func LoadOAuthConfigFromEnv() OAuthConfig {
	return OAuthConfig{
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		AppleClientID:      os.Getenv("APPLE_CLIENT_ID"),
		AppleSecret:        os.Getenv("APPLE_SECRET"),
		AppleTeamID:        os.Getenv("APPLE_TEAM_ID"),
		AppleKeyID:         os.Getenv("APPLE_KEY_ID"),
		CallbackURL:        os.Getenv("OAUTH_CALLBACK_URL"),
		SessionSecret:      os.Getenv("SESSION_SECRET"),
	}
}
