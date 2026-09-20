package calendar

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	providerGoogle   = "google"
	providerCalendly = "calendly"

	googleCalendarScope = "https://www.googleapis.com/auth/calendar.events"
	googleEmailScope    = "https://www.googleapis.com/auth/userinfo.email"
)

var calendlyEndpoint = oauth2.Endpoint{
	AuthURL:  "https://auth.calendly.com/oauth/authorize",
	TokenURL: "https://auth.calendly.com/oauth/token",
}

// OAuthConfig is the calendar-connect credentials. Distinct from sign-in:
// calendar scopes must not ride on the login grant.
type OAuthConfig struct {
	GoogleClientID       string
	GoogleClientSecret   string
	CalendlyClientID     string
	CalendlyClientSecret string
	CallbackBaseURL      string
	StateSecret          string
}

func LoadOAuthConfigFromEnv() OAuthConfig {
	googleID := firstNonEmpty(os.Getenv("GOOGLE_CALENDAR_CLIENT_ID"), os.Getenv("GOOGLE_CLIENT_ID"))
	googleSecret := firstNonEmpty(os.Getenv("GOOGLE_CALENDAR_CLIENT_SECRET"), os.Getenv("GOOGLE_CLIENT_SECRET"))
	return OAuthConfig{
		GoogleClientID:       googleID,
		GoogleClientSecret:   googleSecret,
		CalendlyClientID:     os.Getenv("CALENDLY_CLIENT_ID"),
		CalendlyClientSecret: os.Getenv("CALENDLY_CLIENT_SECRET"),
		CallbackBaseURL:      strings.TrimRight(os.Getenv("OAUTH_CALLBACK_URL"), "/"),
		StateSecret:          os.Getenv("SESSION_SECRET"),
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (c OAuthConfig) googleConfigured() bool {
	return c.GoogleClientID != "" && c.GoogleClientSecret != "" && c.CallbackBaseURL != "" && c.StateSecret != ""
}

func (c OAuthConfig) calendlyConfigured() bool {
	return c.CalendlyClientID != "" && c.CalendlyClientSecret != "" && c.CallbackBaseURL != "" && c.StateSecret != ""
}

func (c OAuthConfig) googleConf() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.GoogleClientID,
		ClientSecret: c.GoogleClientSecret,
		RedirectURL:  c.CallbackBaseURL + "/google-calendar/callback",
		Scopes:       []string{googleCalendarScope, googleEmailScope},
		Endpoint:     google.Endpoint,
	}
}

func (c OAuthConfig) calendlyConf() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.CalendlyClientID,
		ClientSecret: c.CalendlyClientSecret,
		RedirectURL:  c.CallbackBaseURL + "/calendly/callback",
		Scopes:       []string{"default"},
		Endpoint:     calendlyEndpoint,
	}
}

type oauthPayload struct {
	UserID   string `json:"u"`
	Provider string `json:"p"`
	Exp      int64  `json:"e"`
	Nonce    string `json:"n"`
}

func encodeState(secret, userID, provider string, now time.Time) (string, error) {
	if secret == "" {
		return "", errors.New("calendar oauth state secret is not configured")
	}
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("nonce: %w", err)
	}
	p := oauthPayload{
		UserID:   userID,
		Provider: provider,
		Exp:      now.Add(10 * time.Minute).Unix(),
		Nonce:    base64.RawURLEncoding.EncodeToString(nonce),
	}
	body, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	sig := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func decodeState(secret, state string, now time.Time) (userID, provider string, err error) {
	parts := strings.Split(state, ".")
	if len(parts) != 2 {
		return "", "", errors.New("invalid oauth state")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", "", errors.New("invalid oauth state")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", errors.New("invalid oauth state")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), sig) {
		return "", "", errors.New("invalid oauth state")
	}
	var p oauthPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return "", "", errors.New("invalid oauth state")
	}
	if now.Unix() > p.Exp {
		return "", "", errors.New("oauth state expired")
	}
	return p.UserID, p.Provider, nil
}

func (c OAuthConfig) authURL(provider, state string) (string, error) {
	switch provider {
	case providerGoogle:
		if !c.googleConfigured() {
			return "", errProviderOff
		}
		return c.googleConf().AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce), nil
	case providerCalendly:
		if !c.calendlyConfigured() {
			return "", errProviderOff
		}
		return c.calendlyConf().AuthCodeURL(state), nil
	default:
		return "", errUnknownProvider
	}
}

func (c OAuthConfig) exchange(ctx context.Context, provider, code string) (*oauth2.Token, error) {
	switch provider {
	case providerGoogle:
		if !c.googleConfigured() {
			return nil, errProviderOff
		}
		return c.googleConf().Exchange(ctx, code)
	case providerCalendly:
		if !c.calendlyConfigured() {
			return nil, errProviderOff
		}
		return c.calendlyConf().Exchange(ctx, code)
	default:
		return nil, errUnknownProvider
	}
}

func (c OAuthConfig) tokenSource(ctx context.Context, provider, refresh string) (oauth2.TokenSource, error) {
	tok := &oauth2.Token{RefreshToken: refresh}
	switch provider {
	case providerGoogle:
		if !c.googleConfigured() {
			return nil, errProviderOff
		}
		return c.googleConf().TokenSource(ctx, tok), nil
	case providerCalendly:
		if !c.calendlyConfigured() {
			return nil, errProviderOff
		}
		return c.calendlyConf().TokenSource(ctx, tok), nil
	default:
		return nil, errUnknownProvider
	}
}

var (
	errProviderOff     = errors.New("calendar provider is not configured")
	errUnknownProvider = errors.New("unknown calendar provider")
)
