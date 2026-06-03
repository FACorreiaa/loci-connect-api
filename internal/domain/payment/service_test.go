package payment

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	stripe "github.com/stripe/stripe-go/v81"
)

// fakePaymentRepo is an in-memory Repository. Only the methods exercised by
// ProcessStripeEvent capture state; the rest satisfy the interface.
type fakePaymentRepo struct {
	upserted    []*Subscription
	upsertErr   error
	webhookSeen []string
}

func (f *fakePaymentRepo) CreatePayment(context.Context, *Payment) error { return nil }
func (f *fakePaymentRepo) GetPaymentByID(context.Context, uuid.UUID) (*Payment, error) {
	return nil, nil
}
func (f *fakePaymentRepo) GetPaymentByExternalID(context.Context, string) (*Payment, error) {
	return nil, nil
}
func (f *fakePaymentRepo) UpdatePaymentStatus(context.Context, uuid.UUID, string, *string) error {
	return nil
}
func (f *fakePaymentRepo) GetUserPayments(context.Context, uuid.UUID, int, int) ([]Payment, int, error) {
	return nil, 0, nil
}
func (f *fakePaymentRepo) CreateInvoice(context.Context, *Invoice) error { return nil }
func (f *fakePaymentRepo) GetInvoiceByID(context.Context, uuid.UUID) (*Invoice, error) {
	return nil, nil
}
func (f *fakePaymentRepo) GetUserInvoices(context.Context, uuid.UUID, int, int) ([]Invoice, int, error) {
	return nil, 0, nil
}
func (f *fakePaymentRepo) LinkPaymentToInvoice(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}
func (f *fakePaymentRepo) CreateRefund(context.Context, *Refund) error { return nil }
func (f *fakePaymentRepo) GetTotalRefundedAmount(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}
func (f *fakePaymentRepo) RecordWebhookEvent(_ context.Context, eventID, _ string) error {
	f.webhookSeen = append(f.webhookSeen, eventID)
	return nil
}
func (f *fakePaymentRepo) GetSubscriptionByStripeID(context.Context, string) (*Subscription, error) {
	return nil, nil
}
func (f *fakePaymentRepo) GetSubscriptionByUserID(context.Context, uuid.UUID) (*Subscription, error) {
	return nil, nil
}
func (f *fakePaymentRepo) UpdateSubscriptionStatus(context.Context, uuid.UUID, string, *time.Time, *time.Time) error {
	return nil
}
func (f *fakePaymentRepo) UpsertSubscription(_ context.Context, sub *Subscription) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.upserted = append(f.upserted, sub)
	return nil
}

func newTestService(repo Repository) Service {
	return NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)), "sk_test_dummy")
}

func subEvent(t *testing.T, eventType string, sub map[string]any) stripe.Event {
	t.Helper()
	raw, err := json.Marshal(sub)
	if err != nil {
		t.Fatalf("marshal sub: %v", err)
	}
	return stripe.Event{
		ID:   "evt_" + eventType,
		Type: stripe.EventType(eventType),
		Data: &stripe.EventData{Raw: raw},
	}
}

func TestProcessStripeEvent_SubscriptionCreated(t *testing.T) {
	repo := &fakePaymentRepo{}
	svc := newTestService(repo)
	userID := uuid.New()

	ev := subEvent(t, "customer.subscription.created", map[string]any{
		"id":       "sub_123",
		"status":   "active",
		"created":  time.Now().Unix(),
		"metadata": map[string]string{"user_id": userID.String()},
		"items": map[string]any{
			"data": []map[string]any{{"price": map[string]any{"id": "price_1"}}},
		},
	})

	if err := svc.ProcessStripeEvent(context.Background(), ev); err != nil {
		t.Fatalf("ProcessStripeEvent: %v", err)
	}
	if len(repo.upserted) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(repo.upserted))
	}
	got := repo.upserted[0]
	if got.UserID != userID {
		t.Errorf("user id: got %s want %s", got.UserID, userID)
	}
	if got.Status != "active" {
		t.Errorf("status: got %s want active", got.Status)
	}
	if got.Plan != "pro" {
		t.Errorf("plan: got %s want pro (priced item)", got.Plan)
	}
	if got.ExternalSubscriptionID == nil || *got.ExternalSubscriptionID != "sub_123" {
		t.Errorf("external sub id: got %v want sub_123", got.ExternalSubscriptionID)
	}
	if got.ExternalProvider != "stripe" {
		t.Errorf("provider: got %s want stripe", got.ExternalProvider)
	}
}

func TestProcessStripeEvent_MissingUserIDSkips(t *testing.T) {
	repo := &fakePaymentRepo{}
	svc := newTestService(repo)

	ev := subEvent(t, "customer.subscription.updated", map[string]any{
		"id":       "sub_456",
		"status":   "active",
		"metadata": map[string]string{}, // no user_id
	})

	if err := svc.ProcessStripeEvent(context.Background(), ev); err != nil {
		t.Fatalf("missing user_id should be a no-op, got error %v", err)
	}
	if len(repo.upserted) != 0 {
		t.Fatalf("missing user_id must not upsert, got %d", len(repo.upserted))
	}
}

func TestProcessStripeEvent_InvalidUserIDErrors(t *testing.T) {
	repo := &fakePaymentRepo{}
	svc := newTestService(repo)

	ev := subEvent(t, "customer.subscription.created", map[string]any{
		"id":       "sub_789",
		"status":   "active",
		"metadata": map[string]string{"user_id": "not-a-uuid"},
	})

	if err := svc.ProcessStripeEvent(context.Background(), ev); err == nil {
		t.Fatal("invalid user_id should return an error")
	}
	if len(repo.upserted) != 0 {
		t.Fatalf("invalid user_id must not upsert, got %d", len(repo.upserted))
	}
}

func TestProcessStripeEvent_UnhandledTypeNoop(t *testing.T) {
	repo := &fakePaymentRepo{}
	svc := newTestService(repo)

	ev := stripe.Event{ID: "evt_x", Type: "payment_intent.succeeded", Data: &stripe.EventData{Raw: []byte(`{}`)}}
	if err := svc.ProcessStripeEvent(context.Background(), ev); err != nil {
		t.Fatalf("payment_intent.succeeded should be a no-op, got %v", err)
	}
	if len(repo.upserted) != 0 {
		t.Fatalf("unhandled type must not upsert, got %d", len(repo.upserted))
	}
}
