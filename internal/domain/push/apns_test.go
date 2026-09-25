package push

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/pkg/config"
)

// testAPNSKey is a fresh P-256 key in the PEM shape Apple's .p8 uses.
func testAPNSKey(t *testing.T) (pemBytes []byte, key *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), key
}

func testAPNSConfig(t *testing.T) (config.PushConfig, *ecdsa.PrivateKey) {
	t.Helper()
	pemBytes, key := testAPNSKey(t)
	return config.PushConfig{
		APNSKeyID:  "KEY123",
		APNSTeamID: "TEAM456",
		APNSKey:    pemBytes,
		APNSTopics: []string{"com.example.app", "com.example.app.beta"},
	}, key
}

// apnsServer records the last request and answers with the given status
// and body, so the sender's request shape and error mapping can be checked
// without Apple.
type apnsServer struct {
	*httptest.Server
	status int
	body   string
	calls  atomic.Int32
	last   *http.Request
	lastB  []byte
}

func newAPNSServer(t *testing.T, status int, body string) *apnsServer {
	t.Helper()
	s := &apnsServer{status: status, body: body}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		buf := make([]byte, 8192)
		n, _ := r.Body.Read(buf)
		s.lastB = buf[:n]
		s.last = r
		w.WriteHeader(s.status)
		_, _ = w.Write([]byte(s.body))
	}))
	t.Cleanup(s.Close)
	return s
}

func testSender(t *testing.T, srv *apnsServer) *APNSSender {
	t.Helper()
	cfg, _ := testAPNSConfig(t)
	sender, err := NewAPNSSender(cfg, srv.Client())
	require.NoError(t, err)
	sender.hostFor = func(string) string { return srv.URL }
	return sender
}

func apnsDevice() Device {
	return Device{
		ID:              uuid.New(),
		Platform:        PlatformAPNS,
		Endpoint:        "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		APNSTopic:       "com.example.app.beta",
		APNSEnvironment: APNSEnvironmentProduction,
	}
}

func payloadBody(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(BuildPayload(testRun()))
	require.NoError(t, err)
	return body
}

func TestNewAPNSSender_RejectsMissingKey(t *testing.T) {
	_, err := NewAPNSSender(config.PushConfig{APNSKeyID: "k", APNSTeamID: "t"}, nil)
	require.Error(t, err)
	_, err = NewAPNSSender(config.PushConfig{APNSKeyID: "k", APNSTeamID: "t", APNSKey: []byte("not pem")}, nil)
	require.Error(t, err)
}

func TestAPNSSender_RequestShape(t *testing.T) {
	srv := newAPNSServer(t, http.StatusOK, "")
	sender := testSender(t, srv)
	d := apnsDevice()

	gone, err := sender.Send(context.Background(), d, payloadBody(t))
	require.NoError(t, err)
	require.False(t, gone)

	require.Equal(t, "/3/device/"+d.Endpoint, srv.last.URL.Path)
	require.Equal(t, "com.example.app.beta", srv.last.Header.Get("apns-topic"))
	require.Equal(t, "alert", srv.last.Header.Get("apns-push-type"))
	require.Equal(t, "10", srv.last.Header.Get("apns-priority"))
	require.NotEmpty(t, srv.last.Header.Get("apns-expiration"))

	// The alert sits under aps; the routing keys sit at the top level, where
	// the app's SessionLink(userInfo:) reads them.
	var body map[string]any
	require.NoError(t, json.Unmarshal(srv.lastB, &body))
	aps := body["aps"].(map[string]any)
	alert := aps["alert"].(map[string]any)
	require.Equal(t, "Your Lisbon itinerary is ready", alert["title"])
	require.Equal(t, "search", aps["thread-id"])
	require.Equal(t, "itinerary", body["domain"])
	require.Equal(t, "Lisbon", body["cityName"])
	require.Equal(t, "done", body["status"])
	require.NotEmpty(t, body["sessionId"])
}

func TestAPNSSender_ProviderTokenIsES256AndReused(t *testing.T) {
	srv := newAPNSServer(t, http.StatusOK, "")
	sender := testSender(t, srv)
	cfg, key := testAPNSConfig(t)
	parsed, err := jwt.ParseECPrivateKeyFromPEM(cfg.APNSKey)
	require.NoError(t, err)
	sender.key = parsed
	_ = key

	first, err := sender.providerToken()
	require.NoError(t, err)
	second, err := sender.providerToken()
	require.NoError(t, err)
	require.Equal(t, first, second, "token should be cached")

	tok, err := jwt.Parse(first, func(t *jwt.Token) (any, error) { return &parsed.PublicKey, nil })
	require.NoError(t, err)
	require.Equal(t, "ES256", tok.Header["alg"])
	require.Equal(t, "KEY123", tok.Header["kid"])
	claims := tok.Claims.(jwt.MapClaims)
	require.Equal(t, "TEAM456", claims["iss"])

	// Past the lifetime a new token is minted.
	sender.now = func() time.Time { return time.Now().Add(apnsTokenLifetime + time.Minute) }
	third, err := sender.providerToken()
	require.NoError(t, err)
	require.NotEqual(t, first, third)
}

func TestAPNSSender_GoneReasonsRemoveTheDevice(t *testing.T) {
	for _, reason := range []string{"BadDeviceToken", "Unregistered", "DeviceTokenNotForTopic"} {
		srv := newAPNSServer(t, http.StatusBadRequest, `{"reason":"`+reason+`"}`)
		sender := testSender(t, srv)
		gone, err := sender.Send(context.Background(), apnsDevice(), payloadBody(t))
		require.True(t, gone, reason)
		// The reason rides along so the notifier can log why the device left.
		require.ErrorContains(t, err, reason)
	}
	srv := newAPNSServer(t, http.StatusGone, `{"reason":"Unregistered","timestamp":1}`)
	sender := testSender(t, srv)
	gone, err := sender.Send(context.Background(), apnsDevice(), payloadBody(t))
	require.True(t, gone)
	require.ErrorContains(t, err, "Unregistered")
}

func TestAPNSSender_OtherErrorsAreReportedNotGone(t *testing.T) {
	srv := newAPNSServer(t, http.StatusTooManyRequests, `{"reason":"TooManyRequests"}`)
	sender := testSender(t, srv)
	gone, err := sender.Send(context.Background(), apnsDevice(), payloadBody(t))
	require.Error(t, err)
	require.False(t, gone)
	require.Contains(t, err.Error(), "TooManyRequests")
}

func TestAPNSSender_ExpiredProviderTokenRetriesOnce(t *testing.T) {
	srv := newAPNSServer(t, http.StatusForbidden, `{"reason":"ExpiredProviderToken"}`)
	sender := testSender(t, srv)
	_, err := sender.Send(context.Background(), apnsDevice(), payloadBody(t))
	require.Error(t, err)
	require.Equal(t, int32(2), srv.calls.Load(), "one retry with a fresh token, then give up")
}

func TestAPNSSender_DeviceWithoutTopicIsDropped(t *testing.T) {
	srv := newAPNSServer(t, http.StatusOK, "")
	sender := testSender(t, srv)
	d := apnsDevice()
	d.APNSTopic = ""
	gone, err := sender.Send(context.Background(), d, payloadBody(t))
	require.Error(t, err)
	require.True(t, gone)
	require.Equal(t, int32(0), srv.calls.Load())
}

func TestValidAPNSTokenAndEnvironment(t *testing.T) {
	require.True(t, ValidAPNSToken(apnsDevice().Endpoint))
	require.False(t, ValidAPNSToken("short"))
	require.False(t, ValidAPNSToken("zz23456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"))
	require.True(t, ValidAPNSEnvironment(""))
	require.True(t, ValidAPNSEnvironment("sandbox"))
	require.False(t, ValidAPNSEnvironment("staging"))
}
