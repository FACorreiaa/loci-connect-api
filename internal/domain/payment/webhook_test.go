package payment

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/webhook"
)

type recordingEventService struct {
	Service
	calls int
}

func (s *recordingEventService) ProcessStripeEvent(context.Context, stripe.Event) error {
	s.calls++
	return nil
}

func deliver(t *testing.T, svc Service, handlerSecret, signingSecret string) *httptest.ResponseRecorder {
	t.Helper()
	payload := fmt.Sprintf(`{"id":"evt_1","object":"event","type":"charge.refunded","api_version":%q,"data":{"object":{}}}`, stripe.APIVersion)
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: []byte(payload),
		Secret:  signingSecret,
	})
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", strings.NewReader(payload))
	req.Header.Set("Stripe-Signature", signed.Header)
	rec := httptest.NewRecorder()
	WebhookHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)), handlerSecret)(rec, req)
	return rec
}

func TestWebhookHandler_AcceptsAValidSignature(t *testing.T) {
	svc := &recordingEventService{}
	rec := deliver(t, svc, "whsec_real", "whsec_real")
	if rec.Code != http.StatusOK || svc.calls != 1 {
		t.Fatalf("status = %d, calls = %d; want 200 and 1", rec.Code, svc.calls)
	}
}

func TestWebhookHandler_RejectsAWrongSignature(t *testing.T) {
	svc := &recordingEventService{}
	rec := deliver(t, svc, "whsec_real", "whsec_forged")
	if rec.Code != http.StatusBadRequest || svc.calls != 0 {
		t.Fatalf("status = %d, calls = %d; want 400 and 0", rec.Code, svc.calls)
	}
}

// With no secret configured, a signature made with the empty key would verify,
// so anyone could grant themselves a plan. A 503 also makes Stripe retry, so
// real events survive until the secret is set.
func TestWebhookHandler_NoSecretRefusesEvenAMatchingSignature(t *testing.T) {
	svc := &recordingEventService{}
	rec := deliver(t, svc, "", "")
	if rec.Code != http.StatusServiceUnavailable || svc.calls != 0 {
		t.Fatalf("status = %d, calls = %d; want 503 and 0", rec.Code, svc.calls)
	}
}
