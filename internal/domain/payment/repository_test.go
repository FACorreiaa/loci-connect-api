//go:build integration

package payment

import (
	"context"
	"fmt"
	"testing"
	"time"

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

func strptr(s string) *string { return &s }

func TestCreateAndGetPayment(t *testing.T) {
	repo, pool := newPaymentRepo(t)
	ctx := context.Background()
	userID := insertUser(t, pool)

	p := &Payment{
		UserID:            userID,
		Provider:          "stripe",
		ExternalPaymentID: strptr("pi_test_123"),
		Type:              "one_time",
		Amount:            1999,
		Currency:          "usd",
		Status:            "succeeded",
		Description:       strptr("Test charge"),
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

	require.NoError(t, repo.UpdatePaymentStatus(ctx, p.ID, "failed", strptr("card_declined")))

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
		p := &Payment{UserID: userID, Provider: "stripe", Type: "one_time",
			Amount: int64(100 * (i + 1)), Currency: "usd", Status: "succeeded"}
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
		ExternalSubscriptionID: strptr("sub_initial"),
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
		ExternalSubscriptionID: strptr("sub_upgraded"),
	}
	require.NoError(t, repo.UpsertSubscription(ctx, sub2))

	got, err = repo.GetSubscriptionByUserID(ctx, userID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "premium_monthly", got.Plan, "upsert should update plan in place")
	require.NotNil(t, got.ExternalSubscriptionID)
	assert.Equal(t, "sub_upgraded", *got.ExternalSubscriptionID)
}
