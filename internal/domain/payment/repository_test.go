//go:build integration

package payment

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FACorreiaa/go-utils/pkg/util"
	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPaymentRepo(t *testing.T) (Repository, *pgxpool.Pool) {
	t.Helper()
	pool, _ := testsupport.StartPostgres(t)
	testsupport.Truncate(t, pool,
		"payments", "invoices", "refunds", "webhook_events", "subscriptions", "users")
	return NewRepository(pool), pool
}

func insertUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	email := fmt.Sprintf("pay-%s@example.com", uuid.NewString())
	err := pool.QueryRow(context.Background(),
		"INSERT INTO users (email) VALUES ($1) RETURNING id", email).Scan(&id)
	require.NoError(t, err, "insert test user")
	return id
}

func TestCreateAndGetPayment(t *testing.T) {
	repo, pool := newPaymentRepo(t)
	ctx := context.Background()
	userID := insertUser(t, pool)

	p := &Payment{
		UserID:            userID,
		Provider:          "stripe",
		ExternalPaymentID: util.Ptr("pi_test_123"),
		Type:              "one_time",
		Amount:            1999,
		Currency:          "usd",
		Status:            "succeeded",
		Description:       util.Ptr("Test charge"),
		Metadata:          map[string]any{"order": "abc"},
	}
	require.NoError(t, repo.CreatePayment(ctx, p))
	require.NotEqual(t, uuid.Nil, p.ID)

	got, err := repo.GetPaymentByID(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, userID, got.UserID)
	assert.Equal(t, int64(1999), got.Amount)
	assert.Equal(t, "succeeded", got.Status)
	require.NotNil(t, got.ExternalPaymentID)
	assert.Equal(t, "pi_test_123", *got.ExternalPaymentID)
	assert.Equal(t, "abc", got.Metadata["order"])

	byExt, err := repo.GetPaymentByExternalID(ctx, "pi_test_123")
	require.NoError(t, err)
	assert.Equal(t, p.ID, byExt.ID)
}

func TestUpdatePaymentStatus(t *testing.T) {
	repo, pool := newPaymentRepo(t)
	ctx := context.Background()
	userID := insertUser(t, pool)

	p := &Payment{UserID: userID, Provider: "stripe", Type: "one_time", Amount: 500, Currency: "usd", Status: "pending"}
	require.NoError(t, repo.CreatePayment(ctx, p))

	require.NoError(t, repo.UpdatePaymentStatus(ctx, p.ID, "failed", util.Ptr("card_declined")))

	got, err := repo.GetPaymentByID(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", got.Status)
	require.NotNil(t, got.FailureReason)
	assert.Equal(t, "card_declined", *got.FailureReason)
}

func TestGetUserPayments_Pagination(t *testing.T) {
	repo, pool := newPaymentRepo(t)
	ctx := context.Background()
	userID := insertUser(t, pool)

	for i := 0; i < 3; i++ {
		p := &Payment{
			UserID: userID, Provider: "stripe", Type: "one_time",
			Amount: int64(100 * (i + 1)), Currency: "usd", Status: "succeeded",
		}
		require.NoError(t, repo.CreatePayment(ctx, p))
	}

	page, total, err := repo.GetUserPayments(ctx, userID, 2, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Len(t, page, 2)
}

func TestRefunds_TotalCountsSucceededOnly(t *testing.T) {
	repo, pool := newPaymentRepo(t)
	ctx := context.Background()
	userID := insertUser(t, pool)

	p := &Payment{UserID: userID, Provider: "stripe", Type: "one_time", Amount: 5000, Currency: "usd", Status: "succeeded"}
	require.NoError(t, repo.CreatePayment(ctx, p))

	require.NoError(t, repo.CreateRefund(ctx, &Refund{PaymentID: p.ID, AmountCents: 1000, Currency: "usd", Status: "succeeded"}))
	require.NoError(t, repo.CreateRefund(ctx, &Refund{PaymentID: p.ID, AmountCents: 500, Currency: "usd", Status: "succeeded"}))
	require.NoError(t, repo.CreateRefund(ctx, &Refund{PaymentID: p.ID, AmountCents: 9999, Currency: "usd", Status: "pending"}))

	total, err := repo.GetTotalRefundedAmount(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1500), total, "only succeeded refunds count")
}

func TestRecordWebhookEvent_Idempotency(t *testing.T) {
	repo, _ := newPaymentRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.RecordWebhookEvent(ctx, "evt_123", "customer.subscription.created"))

	// Duplicate event_id violates the UNIQUE constraint -> second call errors.
	err := repo.RecordWebhookEvent(ctx, "evt_123", "customer.subscription.created")
	require.Error(t, err, "duplicate webhook event must be rejected for idempotency")
}

// Forgetting an event is what lets Stripe's redelivery be applied. It must free
// that id only, or a later duplicate of another event would be applied twice.
func TestForgetWebhookEvent_FreesOnlyThatEvent(t *testing.T) {
	repo, _ := newPaymentRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.RecordWebhookEvent(ctx, "evt_failed", "invoice.paid"))
	require.NoError(t, repo.RecordWebhookEvent(ctx, "evt_applied", "invoice.paid"))

	require.NoError(t, repo.ForgetWebhookEvent(ctx, "evt_failed"))

	require.NoError(t, repo.RecordWebhookEvent(ctx, "evt_failed", "invoice.paid"), "redelivery must be accepted")
	require.Error(t, repo.RecordWebhookEvent(ctx, "evt_applied", "invoice.paid"), "other events stay recorded")
}

// Forgetting an id that was never recorded is not an error: the cleanup runs
// after a failure and must not add one of its own.
func TestForgetWebhookEvent_UnknownEvent(t *testing.T) {
	repo, _ := newPaymentRepo(t)

	require.NoError(t, repo.ForgetWebhookEvent(context.Background(), "evt_never_seen"))
}

func TestUpsertSubscription_InsertThenUpdate(t *testing.T) {
	repo, pool := newPaymentRepo(t)
	ctx := context.Background()
	userID := insertUser(t, pool)

	sub := &Subscription{
		UserID:                 userID,
		Plan:                   "free",
		Status:                 "active",
		StartDate:              time.Now(),
		ExternalProvider:       "stripe",
		ExternalSubscriptionID: util.Ptr("sub_initial"),
	}
	require.NoError(t, repo.UpsertSubscription(ctx, sub))

	got, err := repo.GetSubscriptionByUserID(ctx, userID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "free", got.Plan)
	assert.Equal(t, "active", got.Status)

	// Same user -> ON CONFLICT (user_id) updates in place.
	// 'premium_monthly' is a valid subscription_plan_type enum value
	// (the enum is free/premium_monthly/premium_annual).
	sub2 := &Subscription{
		UserID:                 userID,
		Plan:                   "premium_monthly",
		Status:                 "active",
		StartDate:              time.Now(),
		ExternalProvider:       "stripe",
		ExternalSubscriptionID: util.Ptr("sub_upgraded"),
	}
	require.NoError(t, repo.UpsertSubscription(ctx, sub2))

	got, err = repo.GetSubscriptionByUserID(ctx, userID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "premium_monthly", got.Plan, "upsert should update plan in place")
	require.NotNil(t, got.ExternalSubscriptionID)
	assert.Equal(t, "sub_upgraded", *got.ExternalSubscriptionID)
}

func TestUpsertSubscription_CustomerIDRoundTripAndSticky(t *testing.T) {
	repo, pool := newPaymentRepo(t)
	ctx := context.Background()
	userID := insertUser(t, pool)

	sub := &Subscription{
		UserID:                 userID,
		Plan:                   "premium_monthly",
		Status:                 "active",
		StartDate:              time.Now(),
		ExternalProvider:       "stripe",
		ExternalSubscriptionID: util.Ptr("sub_1"),
		ExternalCustomerID:     util.Ptr("cus_abc"),
	}
	require.NoError(t, repo.UpsertSubscription(ctx, sub))

	got, err := repo.GetSubscriptionByUserID(ctx, userID)
	require.NoError(t, err)
	require.NotNil(t, got.ExternalCustomerID)
	assert.Equal(t, "cus_abc", *got.ExternalCustomerID)

	// A later event without a customer must not unlink the stored customer.
	sub2 := &Subscription{
		UserID:                 userID,
		Plan:                   "premium_monthly",
		Status:                 "canceled",
		StartDate:              time.Now(),
		ExternalProvider:       "stripe",
		ExternalSubscriptionID: util.Ptr("sub_1"),
	}
	require.NoError(t, repo.UpsertSubscription(ctx, sub2))

	got, err = repo.GetSubscriptionByUserID(ctx, userID)
	require.NoError(t, err)
	require.NotNil(t, got.ExternalCustomerID, "customer link must survive metadata-less events")
	assert.Equal(t, "cus_abc", *got.ExternalCustomerID)
	assert.Equal(t, "canceled", got.Status)
}

func stripeSubAt(userID uuid.UUID, status string, at *time.Time) *Subscription {
	return &Subscription{
		UserID:                 userID,
		Plan:                   "premium_monthly",
		Status:                 status,
		StartDate:              time.Now(),
		ExternalProvider:       "stripe",
		ExternalSubscriptionID: util.Ptr("sub_order"),
		LastEventAt:            at,
	}
}

// Stripe does not deliver in order. A late "updated" must not re-activate a
// subscription that a newer event already cancelled.
func TestUpsertSubscription_OlderEventDoesNotOverwrite(t *testing.T) {
	repo, pool := newPaymentRepo(t)
	ctx := context.Background()
	userID := insertUser(t, pool)
	older, newer := time.Unix(1_700_000_000, 0), time.Unix(1_700_000_100, 0)

	require.NoError(t, repo.UpsertSubscription(ctx, stripeSubAt(userID, "canceled", &newer)))

	err := repo.UpsertSubscription(ctx, stripeSubAt(userID, "active", &older))
	require.ErrorIs(t, err, ErrStaleSubscriptionEvent)

	got, err := repo.GetSubscriptionByUserID(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, "canceled", got.Status)
}

func TestUpsertSubscription_NewerEventApplies(t *testing.T) {
	repo, pool := newPaymentRepo(t)
	ctx := context.Background()
	userID := insertUser(t, pool)
	older, newer := time.Unix(1_700_000_000, 0), time.Unix(1_700_000_100, 0)

	require.NoError(t, repo.UpsertSubscription(ctx, stripeSubAt(userID, "active", &older)))
	require.NoError(t, repo.UpsertSubscription(ctx, stripeSubAt(userID, "canceled", &newer)))

	got, err := repo.GetSubscriptionByUserID(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, "canceled", got.Status)
}

// Stripe's timestamps are whole seconds, and created and updated often share one.
// Those apply in arrival order, or the second would be lost every time.
func TestUpsertSubscription_SameSecondEventApplies(t *testing.T) {
	repo, pool := newPaymentRepo(t)
	ctx := context.Background()
	userID := insertUser(t, pool)
	at := time.Unix(1_700_000_000, 0)

	require.NoError(t, repo.UpsertSubscription(ctx, stripeSubAt(userID, "trialing", &at)))
	require.NoError(t, repo.UpsertSubscription(ctx, stripeSubAt(userID, "active", &at)))

	got, err := repo.GetSubscriptionByUserID(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, "active", got.Status)
}

// A row written before this column existed has no time to compare against. An
// event with no time must not erase the one already stored.
func TestUpsertSubscription_MissingTimes(t *testing.T) {
	repo, pool := newPaymentRepo(t)
	ctx := context.Background()
	userID := insertUser(t, pool)
	at := time.Unix(1_700_000_000, 0)
	earlier := at.Add(-time.Hour)

	require.NoError(t, repo.UpsertSubscription(ctx, stripeSubAt(userID, "active", nil)))
	require.NoError(t, repo.UpsertSubscription(ctx, stripeSubAt(userID, "canceled", &at)), "a row with no time accepts an event")
	require.NoError(t, repo.UpsertSubscription(ctx, stripeSubAt(userID, "active", nil)), "an event with no time still applies")

	err := repo.UpsertSubscription(ctx, stripeSubAt(userID, "canceled", &earlier))
	require.ErrorIs(t, err, ErrStaleSubscriptionEvent, "the stored time must have survived")
}

func TestSetStripeCustomerID_CreatesRowAndResolvesUser(t *testing.T) {
	repo, pool := newPaymentRepo(t)
	ctx := context.Background()
	userID := insertUser(t, pool)

	// No subscription row yet: linker must create a free/active one.
	require.NoError(t, repo.SetStripeCustomerID(ctx, userID, "cus_link"))

	got, err := repo.GetSubscriptionByUserID(ctx, userID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "free", got.Plan)
	require.NotNil(t, got.ExternalCustomerID)
	assert.Equal(t, "cus_link", *got.ExternalCustomerID)

	resolved, err := repo.GetUserIDByStripeCustomerID(ctx, "cus_link")
	require.NoError(t, err)
	assert.Equal(t, userID, resolved)

	unknown, err := repo.GetUserIDByStripeCustomerID(ctx, "cus_missing")
	require.NoError(t, err)
	assert.Equal(t, uuid.Nil, unknown)
}
