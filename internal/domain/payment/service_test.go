package payment

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	stripe "github.com/stripe/stripe-go/v81"
)

// fakePaymentRepo is an in-memory Repository. Only the methods exercised by
// ProcessStripeEvent capture state; the rest satisfy the interface.
type fakePaymentRepo struct {
	upserted        []*Subscription
	upsertErr       error
	webhookSeen     []string
	linkedCustomers map[uuid.UUID]string
	customerToUser  map[string]uuid.UUID
	subscription    *Subscription
	dailyUsage      int
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
	for _, seen := range f.webhookSeen {
		if seen == eventID {
			// Mirror the DB unique constraint on webhook_events.event_id.
			return &pgconn.PgError{Code: "23505"}
		}
	}
	f.webhookSeen = append(f.webhookSeen, eventID)
	return nil
}

func (f *fakePaymentRepo) GetSubscriptionByStripeID(context.Context, string) (*Subscription, error) {
	return nil, nil
}

func (f *fakePaymentRepo) GetSubscriptionByUserID(context.Context, uuid.UUID) (*Subscription, error) {
	return f.subscription, nil
}

func (f *fakePaymentRepo) GetUserIDByStripeCustomerID(_ context.Context, customerID string) (uuid.UUID, error) {
	return f.customerToUser[customerID], nil
}

func (f *fakePaymentRepo) SetStripeCustomerID(_ context.Context, userID uuid.UUID, customerID string) error {
	if f.linkedCustomers == nil {
		f.linkedCustomers = map[uuid.UUID]string{}
	}
	f.linkedCustomers[userID] = customerID
	return nil
}

func (f *fakePaymentRepo) GetDailyUsage(context.Context, uuid.UUID) (int, error) {
	return f.dailyUsage, nil
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

// recordingInvalidator captures plan-cache invalidations.
type recordingInvalidator struct {
	invalidated []uuid.UUID
}

func (r *recordingInvalidator) InvalidatePlan(userID uuid.UUID) {
	r.invalidated = append(r.invalidated, userID)
}

func newTestService(repo Repository) Service {
	svc, _ := newTestServiceWithInvalidator(repo)
	return svc
}

func newTestServiceWithInvalidator(repo Repository) (Service, *recordingInvalidator) {
	inv := &recordingInvalidator{}
	var quota QuotaReader
	if q, ok := repo.(QuotaReader); ok {
		quota = q
	}
	svc := NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)), quota, inv, StripeConfig{
		APIKey:         "sk_test_dummy",
		PriceIDMonthly: "price_monthly",
		PriceIDAnnual:  "price_annual",
		FreeDailyLimit: 10,
	})
	return svc, inv
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
			"data": []map[string]any{
				{"price": map[string]any{"id": "price_1", "recurring": map[string]any{"interval": "month"}}},
			},
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
	if got.Plan != "premium_monthly" {
		t.Errorf("plan: got %s want premium_monthly (monthly price)", got.Plan)
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

func subEventWithInterval(t *testing.T, userID uuid.UUID, interval string) stripe.Event {
	t.Helper()
	return subEvent(t, "customer.subscription.created", map[string]any{
		"id":       "sub_" + interval,
		"status":   "active",
		"created":  time.Now().Unix(),
		"metadata": map[string]string{"user_id": userID.String()},
		"items": map[string]any{
			"data": []map[string]any{
				{"price": map[string]any{"id": "price_1", "recurring": map[string]any{"interval": interval}}},
			},
		},
	})
}

func TestProcessStripeEvent_PlanFromInterval(t *testing.T) {
	cases := map[string]string{
		"month": "premium_monthly",
		"year":  "premium_annual",
		"week":  "free", // unsupported interval falls back
	}
	for interval, wantPlan := range cases {
		t.Run(interval, func(t *testing.T) {
			repo := &fakePaymentRepo{}
			svc := newTestService(repo)
			userID := uuid.New()

			ev := subEventWithInterval(t, userID, interval)
			if err := svc.ProcessStripeEvent(context.Background(), ev); err != nil {
				t.Fatalf("ProcessStripeEvent: %v", err)
			}
			if len(repo.upserted) != 1 {
				t.Fatalf("expected 1 upsert, got %d", len(repo.upserted))
			}
			if got := repo.upserted[0].Plan; got != wantPlan {
				t.Errorf("interval %q: plan = %q, want %q", interval, got, wantPlan)
			}
		})
	}
}

func TestProcessStripeEvent_DuplicateEventProcessedOnce(t *testing.T) {
	repo := &fakePaymentRepo{}
	svc := newTestService(repo)
	userID := uuid.New()

	ev := subEventWithInterval(t, userID, "month")
	if err := svc.ProcessStripeEvent(context.Background(), ev); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if err := svc.ProcessStripeEvent(context.Background(), ev); err != nil {
		t.Fatalf("replayed delivery should be a no-op, got %v", err)
	}
	if len(repo.upserted) != 1 {
		t.Fatalf("replayed event must not re-upsert, got %d upserts", len(repo.upserted))
	}
	if len(repo.webhookSeen) != 1 {
		t.Fatalf("expected 1 recorded webhook event, got %d", len(repo.webhookSeen))
	}
}

func TestProcessStripeEvent_CheckoutCompletedLinksCustomer(t *testing.T) {
	repo := &fakePaymentRepo{}
	svc, inv := newTestServiceWithInvalidator(repo)
	userID := uuid.New()

	ev := subEvent(t, "checkout.session.completed", map[string]any{
		"id":                  "cs_test_1",
		"client_reference_id": userID.String(),
		"customer":            map[string]any{"id": "cus_123"},
	})

	if err := svc.ProcessStripeEvent(context.Background(), ev); err != nil {
		t.Fatalf("ProcessStripeEvent: %v", err)
	}
	if got := repo.linkedCustomers[userID]; got != "cus_123" {
		t.Fatalf("linked customer = %q, want cus_123", got)
	}
	if len(inv.invalidated) != 1 || inv.invalidated[0] != userID {
		t.Fatalf("expected plan invalidation for %s, got %v", userID, inv.invalidated)
	}
}

func TestProcessStripeEvent_ResolvesUserByCustomerFallback(t *testing.T) {
	userID := uuid.New()
	repo := &fakePaymentRepo{customerToUser: map[string]uuid.UUID{"cus_999": userID}}
	svc := newTestService(repo)

	// No metadata user_id, but the customer was linked earlier by
	// checkout.session.completed.
	ev := subEvent(t, "customer.subscription.updated", map[string]any{
		"id":       "sub_fallback",
		"status":   "active",
		"created":  time.Now().Unix(),
		"customer": map[string]any{"id": "cus_999"},
		"items": map[string]any{
			"data": []map[string]any{
				{"price": map[string]any{"id": "price_1", "recurring": map[string]any{"interval": "month"}}},
			},
		},
	})

	if err := svc.ProcessStripeEvent(context.Background(), ev); err != nil {
		t.Fatalf("ProcessStripeEvent: %v", err)
	}
	if len(repo.upserted) != 1 {
		t.Fatalf("expected 1 upsert via customer fallback, got %d", len(repo.upserted))
	}
	if repo.upserted[0].UserID != userID {
		t.Fatalf("user id = %s, want %s", repo.upserted[0].UserID, userID)
	}
	if repo.upserted[0].ExternalCustomerID == nil || *repo.upserted[0].ExternalCustomerID != "cus_999" {
		t.Fatalf("external customer id = %v, want cus_999", repo.upserted[0].ExternalCustomerID)
	}
}

func TestProcessStripeEvent_SubscriptionUpsertInvalidatesPlan(t *testing.T) {
	repo := &fakePaymentRepo{}
	svc, inv := newTestServiceWithInvalidator(repo)
	userID := uuid.New()

	ev := subEventWithInterval(t, userID, "month")
	if err := svc.ProcessStripeEvent(context.Background(), ev); err != nil {
		t.Fatalf("ProcessStripeEvent: %v", err)
	}
	if len(inv.invalidated) != 1 || inv.invalidated[0] != userID {
		t.Fatalf("expected plan invalidation for %s, got %v", userID, inv.invalidated)
	}
}

func TestMapStripeStatus(t *testing.T) {
	cases := map[stripe.SubscriptionStatus]string{
		stripe.SubscriptionStatusActive:            "active",
		stripe.SubscriptionStatusTrialing:          "trialing",
		stripe.SubscriptionStatusPastDue:           "past_due",
		stripe.SubscriptionStatusCanceled:          "canceled",
		stripe.SubscriptionStatusIncomplete:        "expired",
		stripe.SubscriptionStatusIncompleteExpired: "expired",
		stripe.SubscriptionStatusUnpaid:            "expired",
	}
	for in, want := range cases {
		if got := mapStripeStatus(in); got != want {
			t.Errorf("mapStripeStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGetSubscription_FreeDefaultsWithRealUsage(t *testing.T) {
	repo := &fakePaymentRepo{dailyUsage: 7}
	svc := newTestService(repo)

	got, err := svc.GetSubscription(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	if got.Subscription.Plan != "free" {
		t.Fatalf("plan = %q, want free", got.Subscription.Plan)
	}
	if got.RequestsToday != 7 {
		t.Fatalf("requests today = %d, want 7 (real usage, not placeholder)", got.RequestsToday)
	}
	if got.RequestsLimit != 10 {
		t.Fatalf("requests limit = %d, want configured free limit 10", got.RequestsLimit)
	}
}

func TestGetSubscription_ProHidesFairUseCap(t *testing.T) {
	repo := &fakePaymentRepo{
		dailyUsage:   42,
		subscription: &Subscription{UserID: uuid.New(), Plan: "premium_monthly", Status: "active"},
	}
	svc := newTestService(repo)

	got, err := svc.GetSubscription(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	if got.RequestsLimit != UnlimitedLimit {
		t.Fatalf("pro requests limit = %d, want %d (unlimited sentinel)", got.RequestsLimit, UnlimitedLimit)
	}
	if got.RequestsToday != 42 {
		t.Fatalf("requests today = %d, want 42", got.RequestsToday)
	}
}
