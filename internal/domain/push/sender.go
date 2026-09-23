package push

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/FACorreiaa/loci-connect-api/pkg/config"
)

type Sender interface {
	Send(ctx context.Context, d Device, body []byte) (gone bool, err error)
}

type WebPushSender struct {
	cfg    config.PushConfig
	client *http.Client
}

// NewHTTPClient is the client pushes go out on. It never follows a redirect:
// the endpoint was checked against the push-service allow-list, and a 3xx
// from it must not steer the server's request anywhere else. The sender
// treats the 3xx itself as a failed send.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func NewWebPushSender(cfg config.PushConfig, client *http.Client) *WebPushSender {
	if client == nil {
		client = NewHTTPClient()
	}
	return &WebPushSender{cfg: cfg, client: client}
}

func (s *WebPushSender) Send(ctx context.Context, d Device, body []byte) (bool, error) {
	resp, err := webpush.SendNotificationWithContext(ctx, body, &webpush.Subscription{
		Endpoint: d.Endpoint,
		Keys:     webpush.Keys{P256dh: d.P256dh, Auth: d.Auth},
	}, &webpush.Options{
		HTTPClient:      s.client,
		Subscriber:      s.cfg.VAPIDSubject,
		VAPIDPublicKey:  s.cfg.VAPIDPublicKey,
		VAPIDPrivateKey: s.cfg.VAPIDPrivateKey,
		TTL:             3600,
		Urgency:         webpush.UrgencyHigh,
	})
	if err != nil {
		return false, fmt.Errorf("web push: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return true, nil
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return false, nil
	default:
		return false, fmt.Errorf("web push: status %d", resp.StatusCode)
	}
}
