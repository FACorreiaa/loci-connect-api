package push

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/FACorreiaa/loci-connect-api/pkg/config"
)

// APNs environments a device can belong to. App Store and TestFlight builds
// hold production tokens; a build signed with a development profile holds a
// sandbox token, and Apple drops a token sent to the other host without an
// error, so the device tells us which it has when it registers.
const (
	APNSEnvironmentProduction = "production"
	APNSEnvironmentSandbox    = "sandbox"
)

var apnsHosts = map[string]string{
	APNSEnvironmentProduction: "https://api.push.apple.com",
	APNSEnvironmentSandbox:    "https://api.sandbox.push.apple.com",
}

// ValidAPNSEnvironment says whether a client-supplied environment is one we
// have a host for. Empty means production, the common case.
func ValidAPNSEnvironment(env string) bool {
	_, ok := apnsHosts[normaliseAPNSEnvironment(env)]
	return ok
}

func normaliseAPNSEnvironment(env string) string {
	if env == "" {
		return APNSEnvironmentProduction
	}
	return env
}

// ValidAPNSToken is the 32-byte device token as iOS hex-encodes it.
func ValidAPNSToken(token string) bool {
	if len(token) != 64 {
		return false
	}
	for _, c := range token {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// apnsTokenLifetime is how long one provider JWT is reused. Apple accepts a
// token for up to an hour and asks that it not be refreshed more often than
// every 20 minutes; 50 minutes keeps clear of both.
const apnsTokenLifetime = 50 * time.Minute

// APNSSender delivers a run's payload to iPhones over APNs. It speaks HTTP/2
// through the standard library (net/http negotiates h2 over TLS on its own)
// and signs each request with a provider token: an ES256 JWT over the team's
// push key, the same key for every app under the team.
type APNSSender struct {
	keyID  string
	teamID string
	key    *ecdsa.PrivateKey
	client *http.Client
	// hostFor resolves an environment to a base URL; tests point it at a
	// local server.
	hostFor func(environment string) string
	now     func() time.Time

	mu       sync.Mutex
	token    string
	issuedAt time.Time
}

// NewAPNSSender parses the PEM push key from the config. It fails loudly at
// startup rather than on the first send, when nobody is watching.
func NewAPNSSender(cfg config.PushConfig, client *http.Client) (*APNSSender, error) {
	if !cfg.APNSEnabled() {
		return nil, errors.New("apns: key id, team id and key are all required")
	}
	key, err := jwt.ParseECPrivateKeyFromPEM(cfg.APNSKey)
	if err != nil {
		return nil, fmt.Errorf("apns: parse push key: %w", err)
	}
	if client == nil {
		client = NewHTTPClient()
	}
	return &APNSSender{
		keyID:  cfg.APNSKeyID,
		teamID: cfg.APNSTeamID,
		key:    key,
		client: client,
		hostFor: func(environment string) string {
			return apnsHosts[normaliseAPNSEnvironment(environment)]
		},
		now: time.Now,
	}, nil
}

// apnsBody is the JSON Apple expects: the alert under "aps", and the same
// custom keys the web payload carries at the top level, which is where the
// app's SessionLink(userInfo:) reads them.
type apnsBody struct {
	APS         apnsAPS `json:"aps"`
	SessionID   string  `json:"sessionId"`
	CityName    string  `json:"cityName"`
	Domain      string  `json:"domain"`
	Status      string  `json:"status,omitempty"`
	URL         string  `json:"url"`
	MessageID   string  `json:"messageId,omitempty"`
	Origin      string  `json:"origin,omitempty"`
	SourceLabel string  `json:"sourceLabel,omitempty"`
}

type apnsAPS struct {
	Alert    apnsAlert `json:"alert"`
	Sound    string    `json:"sound"`
	ThreadID string    `json:"thread-id"`
	Category string    `json:"category,omitempty"`
}

type apnsAlert struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// EncodeAPNS turns the shared Payload into the APNs body.
func EncodeAPNS(p Payload) ([]byte, error) {
	threadID := p.ThreadID
	if threadID == "" {
		threadID = "search"
	}
	return json.Marshal(apnsBody{
		APS: apnsAPS{
			Alert:    apnsAlert{Title: p.Title, Body: p.Body},
			Sound:    "default",
			ThreadID: threadID,
			Category: p.Category,
		},
		SessionID:   p.SessionID,
		CityName:    p.CityName,
		Domain:      p.Domain,
		Status:      p.Status,
		URL:         p.URL,
		MessageID:   p.MessageID,
		Origin:      p.Origin,
		SourceLabel: p.SourceLabel,
	})
}

// Send posts one notification. `body` is the marshalled Payload the notifier
// builds for every platform; it is re-shaped for APNs here. Returns gone for
// a token Apple says it will never deliver to again, so the device row is
// removed, the same way a web push endpoint that answers 410 is.
func (s *APNSSender) Send(ctx context.Context, d Device, body []byte) (bool, error) {
	var payload Payload
	if err := json.Unmarshal(body, &payload); err != nil {
		return false, fmt.Errorf("apns: decode payload: %w", err)
	}
	encoded, err := EncodeAPNS(payload)
	if err != nil {
		return false, fmt.Errorf("apns: encode: %w", err)
	}
	if d.APNSTopic == "" {
		return true, errors.New("apns: device has no topic")
	}
	gone, err := s.post(ctx, d, encoded, true)
	return gone, err
}

func (s *APNSSender) post(ctx context.Context, d Device, encoded []byte, retryOnExpiredToken bool) (bool, error) {
	token, err := s.providerToken()
	if err != nil {
		return false, err
	}
	url := s.hostFor(d.APNSEnvironment) + "/3/device/" + d.Endpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return false, fmt.Errorf("apns: build request: %w", err)
	}
	req.Header.Set("authorization", "bearer "+token)
	req.Header.Set("apns-topic", d.APNSTopic)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")
	req.Header.Set("apns-expiration", strconv.FormatInt(s.now().Add(time.Hour).Unix(), 10))
	req.Header.Set("content-type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("apns: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return false, nil
	}
	var apnsErr struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(raw, &apnsErr)
	switch apnsErr.Reason {
	case "BadDeviceToken", "Unregistered", "DeviceTokenNotForTopic", "ExpiredToken":
		return true, nil
	case "ExpiredProviderToken", "InvalidProviderToken":
		if retryOnExpiredToken {
			s.mu.Lock()
			s.token = ""
			s.mu.Unlock()
			return s.post(ctx, d, encoded, false)
		}
	}
	if resp.StatusCode == http.StatusGone {
		return true, nil
	}
	if apnsErr.Reason == "" {
		return false, fmt.Errorf("apns: status %d", resp.StatusCode)
	}
	return false, fmt.Errorf("apns: status %d (%s)", resp.StatusCode, apnsErr.Reason)
}

// providerToken returns a cached ES256 JWT, minting a new one when the old
// is near Apple's one-hour ceiling.
func (s *APNSSender) providerToken() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.token != "" && now.Sub(s.issuedAt) < apnsTokenLifetime {
		return s.token, nil
	}
	claims := jwt.MapClaims{"iss": s.teamID, "iat": now.Unix()}
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	tok.Header["kid"] = s.keyID
	signed, err := tok.SignedString(s.key)
	if err != nil {
		return "", fmt.Errorf("apns: sign provider token: %w", err)
	}
	s.token, s.issuedAt = signed, now
	return signed, nil
}
