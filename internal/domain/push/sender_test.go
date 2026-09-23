package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/pkg/config"
)

// testSubscription builds a real (P256dh, auth) pair the way a browser's
// PushSubscription.getKey() would hand them over, so the sender exercises
// its real web-push encryption rather than a stub.
func testSubscription(t *testing.T) (p256dh, auth string) {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	p256dh = base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())

	authSecret := make([]byte, 16)
	_, err = rand.Read(authSecret)
	require.NoError(t, err)
	auth = base64.RawURLEncoding.EncodeToString(authSecret)
	return p256dh, auth
}

func testPushConfig(t *testing.T) config.PushConfig {
	t.Helper()
	priv, pub, err := webpush.GenerateVAPIDKeys()
	require.NoError(t, err)
	return config.PushConfig{
		VAPIDPublicKey:  pub,
		VAPIDPrivateKey: priv,
		VAPIDSubject:    "mailto:ops@example.com",
	}
}

func TestSenderSendSuccess(t *testing.T) {
	p256dh, auth := testSubscription(t)
	var gotTTL, gotUrgency string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTTL = r.Header.Get("TTL")
		gotUrgency = r.Header.Get("Urgency")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	sender := NewWebPushSender(testPushConfig(t), srv.Client())
	d := Device{ID: uuid.New(), Endpoint: srv.URL, P256dh: p256dh, Auth: auth}

	gone, err := sender.Send(context.Background(), d, []byte(`{"title":"hi"}`))
	require.NoError(t, err)
	require.False(t, gone)
	require.Equal(t, "3600", gotTTL)
	require.Equal(t, "high", gotUrgency)
}

func TestSenderSendGone(t *testing.T) {
	p256dh, auth := testSubscription(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer srv.Close()

	sender := NewWebPushSender(testPushConfig(t), srv.Client())
	d := Device{ID: uuid.New(), Endpoint: srv.URL, P256dh: p256dh, Auth: auth}

	gone, err := sender.Send(context.Background(), d, []byte(`{"title":"hi"}`))
	require.NoError(t, err)
	require.True(t, gone)
}

func TestSenderSendServerError(t *testing.T) {
	p256dh, auth := testSubscription(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	sender := NewWebPushSender(testPushConfig(t), srv.Client())
	d := Device{ID: uuid.New(), Endpoint: srv.URL, P256dh: p256dh, Auth: auth}

	gone, err := sender.Send(context.Background(), d, []byte(`{"title":"hi"}`))
	require.Error(t, err)
	require.False(t, gone)
}

// A push endpoint passed the allow-list; a redirect from it must not send the
// server's request to a host that never did.
func TestSenderDoesNotFollowRedirects(t *testing.T) {
	p256dh, auth := testSubscription(t)
	hits := 0
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusCreated)
	}))
	defer elsewhere.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	sender := NewWebPushSender(testPushConfig(t), NewHTTPClient())
	d := Device{ID: uuid.New(), Endpoint: srv.URL, P256dh: p256dh, Auth: auth}

	gone, err := sender.Send(context.Background(), d, []byte(`{"title":"hi"}`))
	require.Error(t, err, "a 3xx is not a delivered push")
	require.False(t, gone)
	require.Zero(t, hits, "the redirect target was never contacted")
}
