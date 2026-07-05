package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	subquota "github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	stripe "github.com/stripe/stripe-go/v81"
	portalsession "github.com/stripe/stripe-go/v81/billingportal/session"
	checkoutsession "github.com/stripe/stripe-go/v81/checkout/session"
	"github.com/stripe/stripe-go/v81/customer"
	"github.com/stripe/stripe-go/v81/paymentintent"
	"github.com/stripe/stripe-go/v81/refund"
	"github.com/stripe/stripe-go/v81/subscription"
)

type Service interface {
	CreatePayment(ctx context.Context, req *CreatePaymentParams) (*CreatePaymentResult, error)
	GetPayment(ctx context.Context, paymentID string) (*Payment, error)
	GetUserPayments(ctx context.Context, userID uuid.UUID, page, pageSize int) ([]Payment, int, error)
	RefundPayment(ctx context.Context, paymentID string, amountCents int64) (*Refund, error)
	GetInvoice(ctx context.Context, invoiceID string) (*Invoice, error)
	GetUserInvoices(ctx context.Context, userID uuid.UUID, page, pageSize int) ([]Invoice, int, error)

	CreateSubscription(ctx context.Context, req *CreateSubscriptionParams) (*CreateSubscriptionResult, error)
	CancelSubscription(ctx context.Context, subscriptionID string, cancelAtPeriodEnd bool) error
	GetSubscription(ctx context.Context, userID uuid.UUID) (*SubscriptionWithUsage, error)
	ProcessStripeEvent(ctx context.Context, event stripe.Event) error

	// Checkout & Portal
	CreateCheckoutSession(ctx context.Context, req *CreateCheckoutSessionParams) (*CreateCheckoutSessionResult, error)
	CreateCustomerPortalSession(ctx context.Context, userID uuid.UUID, returnURL string) (*CustomerPortalResult, error)
}

// QuotaReader exposes today's LLM usage for subscription reporting.
type QuotaReader interface {
	GetDailyUsage(ctx context.Context, userID uuid.UUID) (int, error)
}

// PlanInvalidator evicts a user's cached plan after a webhook-driven
// entitlement change so upgrades take effect immediately.
type PlanInvalidator interface {
	InvalidatePlan(userID uuid.UUID)
}

// StripeConfig carries the Stripe wiring and quota display values the
// payment service needs.
type StripeConfig struct {
	APIKey         string
	PriceIDMonthly string
	PriceIDAnnual  string
	FreeDailyLimit int
}

type service struct {
	repo        Repository
	logger      *slog.Logger
	quota       QuotaReader
	invalidator PlanInvalidator
	cfg         StripeConfig
}

func NewService(repo Repository, logger *slog.Logger, quota QuotaReader, invalidator PlanInvalidator, cfg StripeConfig) Service {
	stripe.Key = cfg.APIKey
	return &service{
		repo:        repo,
		logger:      logger,
		quota:       quota,
		invalidator: invalidator,
		cfg:         cfg,
	}
}

// Params & Results

type CreatePaymentParams struct {
	UserID      string
	Amount      int64
	Currency    string
	Type        string
	Description string
	Metadata    map[string]string
}

type CreatePaymentResult struct {
	PaymentID         string
	ClientSecret      string
	ExternalPaymentID string
	Status            string
}

type CreateSubscriptionParams struct {
	UserID          string
	Email           string // Added for Customer creation
	PlanID          string // Price ID in Stripe
	PaymentMethodID string
}

type CreateSubscriptionResult struct {
	SubscriptionID string
	ClientSecret   string
	Status         string
}

type CreateCheckoutSessionParams struct {
	UserID     uuid.UUID
	Email      string
	PriceID    string
	SuccessURL string
	CancelURL  string
}

type CreateCheckoutSessionResult struct {
	SessionID string
	URL       string
}

type CustomerPortalResult struct {
	SessionID string
	URL       string
}

type SubscriptionWithUsage struct {
	Subscription        *Subscription
	RequestsToday       int32
	RequestsLimit       int32
	SavedLocations      int32
	SavedLocationsLimit int32
}

// Implementation

func paginationBounds(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return pageSize, (page - 1) * pageSize
}

func (s *service) CreatePayment(ctx context.Context, req *CreatePaymentParams) (*CreatePaymentResult, error) {
	// Call Stripe
	params := &stripe.PaymentIntentParams{
		Amount:      stripe.Int64(req.Amount),
		Currency:    stripe.String(req.Currency),
		Description: stripe.String(req.Description),
		AutomaticPaymentMethods: &stripe.PaymentIntentAutomaticPaymentMethodsParams{
			Enabled: stripe.Bool(true),
		},
	}
	// Add metadata
	if req.Metadata != nil {
		for k, v := range req.Metadata {
			params.AddMetadata(k, v)
		}
	}
	// Add UserID to metadata for webhook correlation
	params.AddMetadata("user_id", req.UserID)

	pi, err := paymentintent.New(params)
	if err != nil {
		s.logger.Error("failed to create payment intent", "error", err)
		return nil, fmt.Errorf("fake stripe error: %w", err)
	}

	// Prepare DB record
	uid, err := uuid.Parse(req.UserID)
	if err != nil {
		return nil, fmt.Errorf("invalid user id: %w", err)
	}

	metaMap := make(map[string]any)
	for k, v := range req.Metadata {
		metaMap[k] = v
	}

	payment := &Payment{
		UserID:            uid,
		Provider:          "stripe",
		ExternalPaymentID: &pi.ID,
		Type:              req.Type,
		Amount:            req.Amount,
		Currency:          req.Currency,
		Status:            string(pi.Status),
		Description:       &req.Description,
		Metadata:          metaMap,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	if err := s.repo.CreatePayment(ctx, payment); err != nil {
		return nil, fmt.Errorf("failed to save payment: %w", err)
	}

	clientSecret := pi.ClientSecret

	return &CreatePaymentResult{
		PaymentID:         payment.ID.String(),
		ClientSecret:      clientSecret,
		ExternalPaymentID: pi.ID,
		Status:            string(pi.Status),
	}, nil
}

func (s *service) GetPayment(ctx context.Context, paymentID string) (*Payment, error) {
	uid, err := uuid.Parse(paymentID)
	if err != nil {
		return nil, fmt.Errorf("invalid payment id")
	}
	return s.repo.GetPaymentByID(ctx, uid)
}

func (s *service) GetUserPayments(ctx context.Context, userID uuid.UUID, page, pageSize int) ([]Payment, int, error) {
	limit, offset := paginationBounds(page, pageSize)
	return s.repo.GetUserPayments(ctx, userID, limit, offset)
}

func (s *service) RefundPayment(ctx context.Context, paymentID string, amountCents int64) (*Refund, error) {
	// 1. Get Payment from DB
	pUID, err := uuid.Parse(paymentID)
	if err != nil {
		return nil, fmt.Errorf("invalid payment id")
	}
	payment, err := s.repo.GetPaymentByID(ctx, pUID)
	if err != nil {
		return nil, fmt.Errorf("payment not found: %w", err)
	}

	if payment.ExternalPaymentID == nil {
		return nil, fmt.Errorf("cannot refund payment without external ID")
	}

	// 2. Call Stripe
	params := &stripe.RefundParams{
		PaymentIntent: stripe.String(*payment.ExternalPaymentID),
	}
	if amountCents > 0 {
		params.Amount = stripe.Int64(amountCents)
	}

	ref, err := refund.New(params)
	if err != nil {
		return nil, fmt.Errorf("stripe refund failed: %w", err)
	}

	// 3. Save Refund to DB
	refundRecord := &Refund{
		PaymentID:      payment.ID,
		StripeRefundID: &ref.ID,
		AmountCents:    ref.Amount,
		Currency:       string(ref.Currency),
		Status:         string(ref.Status),
		CreatedAt:      time.Now(),
	}
	if err := s.repo.CreateRefund(ctx, refundRecord); err != nil {
		return nil, fmt.Errorf("failed to save refund: %w", err)
	}

	// Update payment status if full refund? Webhook handles this usually.
	return refundRecord, nil
}

func (s *service) GetInvoice(ctx context.Context, invoiceID string) (*Invoice, error) {
	uid, err := uuid.Parse(invoiceID)
	if err != nil {
		return nil, fmt.Errorf("invalid invoice id")
	}
	return s.repo.GetInvoiceByID(ctx, uid)
}

func (s *service) GetUserInvoices(ctx context.Context, userID uuid.UUID, page, pageSize int) ([]Invoice, int, error) {
	limit, offset := paginationBounds(page, pageSize)
	return s.repo.GetUserInvoices(ctx, userID, limit, offset)
}

// Subscriptions

func (s *service) CreateSubscription(ctx context.Context, req *CreateSubscriptionParams) (*CreateSubscriptionResult, error) {
	// 1. Find or Create Customer
	// Ideally, we check DB for existing Stripe Customer ID.
	// Since I don't have UserRepo here, I'll assume we try to find by email or create new.
	// In a real app, I'd fetch User, check `user_providers`.
	// For this migration, I'll search Stripe by email.
	params := &stripe.CustomerSearchParams{}
	params.Query = fmt.Sprintf("email:'%s'", req.Email)
	iter := customer.Search(params)

	var customerID string
	if iter.Next() {
		customerID = iter.Customer().ID
	} else {
		// Create
		cParams := &stripe.CustomerParams{
			Email:    stripe.String(req.Email),
			Metadata: map[string]string{"user_id": req.UserID},
		}
		cust, err := customer.New(cParams)
		if err != nil {
			return nil, fmt.Errorf("failed to create stripe customer: %w", err)
		}
		customerID = cust.ID
	}

	// 2. Create Subscription
	subParams := &stripe.SubscriptionParams{
		Customer: stripe.String(customerID),
		Items: []*stripe.SubscriptionItemsParams{
			{
				Price: stripe.String(req.PlanID),
			},
		},
		PaymentBehavior: stripe.String("default_incomplete"),
		PaymentSettings: &stripe.SubscriptionPaymentSettingsParams{
			SaveDefaultPaymentMethod: stripe.String("on_subscription"),
		},
		Expand: []*string{stripe.String("latest_invoice.payment_intent")},
	}
	// Add metadata
	subParams.AddMetadata("user_id", req.UserID)

	sub, err := subscription.New(subParams)
	if err != nil {
		return nil, fmt.Errorf("failed to create subscription: %w", err)
	}

	// 3. Extract Client Secret
	clientSecret := ""
	if sub.LatestInvoice != nil && sub.LatestInvoice.PaymentIntent != nil {
		clientSecret = sub.LatestInvoice.PaymentIntent.ClientSecret
	}

	// 4. Save to DB (optional: Webhook handles this, but saving early is good for ID mapping)
	// We already have CreateSubscription in repo? No, I need to add that logic to Repo or rely on Webhook.
	// The user wants Webhook to handle everything. I'll rely on webhook for persistence to avoid race conditions.

	return &CreateSubscriptionResult{
		SubscriptionID: sub.ID,
		ClientSecret:   clientSecret,
		Status:         string(sub.Status),
	}, nil
}

func (s *service) CancelSubscription(ctx context.Context, subscriptionID string, cancelAtPeriodEnd bool) error {
	// 1. Cancel in Stripe
	params := &stripe.SubscriptionCancelParams{}
	if cancelAtPeriodEnd {
		// Update to cancel at period end
		updateParams := &stripe.SubscriptionParams{
			CancelAtPeriodEnd: stripe.Bool(true),
		}
		_, err := subscription.Update(subscriptionID, updateParams)
		return err
	} else {
		// Immediate cancellation
		_, err := subscription.Cancel(subscriptionID, params)
		return err
	}
	// Webhook will update DB status
}

// planFromStripeItems maps the first subscription item's billing interval to a
// subscription_plan_type enum value (free / premium_monthly / premium_annual).
func planFromStripeItems(items *stripe.SubscriptionItemList) string {
	if items == nil || len(items.Data) == 0 || items.Data[0].Price == nil {
		return "free"
	}
	recurring := items.Data[0].Price.Recurring
	if recurring == nil {
		return "free"
	}
	switch recurring.Interval {
	case stripe.PriceRecurringIntervalMonth:
		return "premium_monthly"
	case stripe.PriceRecurringIntervalYear:
		return "premium_annual"
	default:
		return "free"
	}
}

// mapStripeStatus maps Stripe subscription statuses onto the DB
// subscription_status enum (active, trialing, past_due, canceled, expired).
// Writing an unmapped Stripe status (incomplete, unpaid, paused, ...) would
// violate the enum constraint; those all mean "not entitled", i.e. expired.
func mapStripeStatus(status stripe.SubscriptionStatus) string {
	switch status {
	case stripe.SubscriptionStatusActive:
		return "active"
	case stripe.SubscriptionStatusTrialing:
		return "trialing"
	case stripe.SubscriptionStatusPastDue:
		return "past_due"
	case stripe.SubscriptionStatusCanceled:
		return "canceled"
	default:
		return "expired"
	}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// resolveSubscriptionUser finds the local user for a Stripe subscription:
// metadata user_id first (set on SubscriptionData at checkout), then the
// customer link recorded by checkout.session.completed.
func (s *service) resolveSubscriptionUser(ctx context.Context, stripeSub *stripe.Subscription) (uuid.UUID, error) {
	if userIDStr := stripeSub.Metadata["user_id"]; userIDStr != "" {
		uid, err := uuid.Parse(userIDStr)
		if err != nil {
			return uuid.Nil, fmt.Errorf("invalid user_id in metadata: %w", err)
		}
		return uid, nil
	}
	if stripeSub.Customer != nil && stripeSub.Customer.ID != "" {
		return s.repo.GetUserIDByStripeCustomerID(ctx, stripeSub.Customer.ID)
	}
	return uuid.Nil, nil
}

func (s *service) invalidatePlan(userID uuid.UUID) {
	if s.invalidator != nil {
		s.invalidator.InvalidatePlan(userID)
	}
}

func (s *service) ProcessStripeEvent(ctx context.Context, event stripe.Event) error {
	// Idempotency gate: the unique event_id makes replayed webhook deliveries
	// no-ops before any state is touched.
	if err := s.repo.RecordWebhookEvent(ctx, event.ID, string(event.Type)); err != nil {
		if isUniqueViolation(err) {
			s.logger.Info("skipping already-processed webhook event", "id", event.ID, "type", event.Type)
			return nil
		}
		return fmt.Errorf("failed to record webhook event: %w", err)
	}

	switch event.Type {
	case "payment_intent.succeeded":
		// Handle one-time payments if needed
		s.logger.Info("Payment succeeded", "id", event.ID)

	case "checkout.session.completed":
		// Fallback user<->customer linker: even if the subscription event
		// races or lacks metadata, the checkout session always carries our
		// client_reference_id and the Stripe customer.
		var sess stripe.CheckoutSession
		if err := json.Unmarshal(event.Data.Raw, &sess); err != nil {
			return fmt.Errorf("error parsing checkout session event: %w", err)
		}
		if sess.ClientReferenceID == "" || sess.Customer == nil || sess.Customer.ID == "" {
			s.logger.Warn("checkout session missing client_reference_id or customer", "session", sess.ID)
			return nil
		}
		uid, err := uuid.Parse(sess.ClientReferenceID)
		if err != nil {
			return fmt.Errorf("invalid client_reference_id: %w", err)
		}
		if err := s.repo.SetStripeCustomerID(ctx, uid, sess.Customer.ID); err != nil {
			return fmt.Errorf("failed to link stripe customer: %w", err)
		}
		s.invalidatePlan(uid)
		s.logger.Info("Linked Stripe customer", "user_id", uid, "customer", sess.Customer.ID)

	case "customer.subscription.created", "customer.subscription.updated", "customer.subscription.deleted":
		var stripeSub stripe.Subscription
		if err := json.Unmarshal(event.Data.Raw, &stripeSub); err != nil {
			return fmt.Errorf("error parsing subscription event: %w", err)
		}

		uid, err := s.resolveSubscriptionUser(ctx, &stripeSub)
		if err != nil {
			return err
		}
		if uid == uuid.Nil {
			s.logger.Warn("Subscription event has no resolvable user", "stripe_id", stripeSub.ID)
			return nil // Can't link to user
		}

		internalStatus := mapStripeStatus(stripeSub.Status)

		// Map dates
		startDate := time.Unix(stripeSub.Created, 0)
		var endDate *time.Time
		if stripeSub.CancelAt > 0 {
			t := time.Unix(stripeSub.CancelAt, 0)
			endDate = &t
		} else if stripeSub.EndedAt > 0 {
			t := time.Unix(stripeSub.EndedAt, 0)
			endDate = &t
		}

		var trialEnd *time.Time
		if stripeSub.TrialEnd > 0 {
			t := time.Unix(stripeSub.TrialEnd, 0)
			trialEnd = &t
		}

		// Map the Stripe price's billing interval to our subscription_plan_type
		// enum (free / premium_monthly / premium_annual). Writing an invalid
		// value (the old hardcoded "pro") fails the enum constraint.
		planName := planFromStripeItems(stripeSub.Items)

		sub := &Subscription{
			UserID:                 uid,
			Plan:                   planName,
			Status:                 internalStatus,
			StartDate:              startDate,
			EndDate:                endDate,
			TrialEndDate:           trialEnd,
			ExternalProvider:       "stripe",
			ExternalSubscriptionID: &stripeSub.ID,
		}
		if stripeSub.Customer != nil && stripeSub.Customer.ID != "" {
			sub.ExternalCustomerID = &stripeSub.Customer.ID
		}

		if err := s.repo.UpsertSubscription(ctx, sub); err != nil {
			return fmt.Errorf("failed to upsert subscription: %w", err)
		}
		s.invalidatePlan(uid)
		s.logger.Info("Subscription processed", "user_id", uid, "status", internalStatus, "plan", planName)

	case "invoice.payment_succeeded":
		var inv stripe.Invoice
		if err := json.Unmarshal(event.Data.Raw, &inv); err != nil {
			return fmt.Errorf("error parsing invoice: %w", err)
		}
		if inv.Subscription != nil {
			// Subscription renewal
			// We might want to extend the end date?
			// Usually `customer.subscription.updated` handles the period update.
			// But we can ensure status is active.
			s.logger.Info("Invoice payment succeeded for subscription", "sub_id", inv.Subscription.ID)
		}

	default:
		// s.logger.Debug("Unhandled event type", "type", event.Type)
	}
	return nil
}

// allowedPrice restricts checkout to the configured Pro prices so clients
// cannot pass arbitrary price IDs. When no prices are configured (local dev
// without Stripe setup), validation is skipped with a warning.
func (s *service) allowedPrice(priceID string) bool {
	if s.cfg.PriceIDMonthly == "" && s.cfg.PriceIDAnnual == "" {
		s.logger.Warn("no Stripe price IDs configured; skipping price validation")
		return priceID != ""
	}
	return priceID == s.cfg.PriceIDMonthly || priceID == s.cfg.PriceIDAnnual
}

// CreateCheckoutSession creates a Stripe Checkout Session for the Pro subscription
func (s *service) CreateCheckoutSession(ctx context.Context, req *CreateCheckoutSessionParams) (*CreateCheckoutSessionResult, error) {
	if !s.allowedPrice(req.PriceID) {
		return nil, fmt.Errorf("unknown price id")
	}

	// Get or create customer
	customerID, err := s.getOrCreateStripeCustomer(req.Email, req.UserID.String())
	if err != nil {
		return nil, fmt.Errorf("failed to get/create customer: %w", err)
	}

	params := &stripe.CheckoutSessionParams{
		Customer:          stripe.String(customerID),
		ClientReferenceID: stripe.String(req.UserID.String()),
		Mode:              stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		SuccessURL:        stripe.String(req.SuccessURL),
		CancelURL:         stripe.String(req.CancelURL),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				Price:    stripe.String(req.PriceID),
				Quantity: stripe.Int64(1),
			},
		},
		// Metadata on SubscriptionData propagates to the subscription object,
		// which is what customer.subscription.* webhooks carry. Session-level
		// metadata alone never reaches those events, which left paying users
		// unlinked and never upgraded.
		SubscriptionData: &stripe.CheckoutSessionSubscriptionDataParams{
			Metadata: map[string]string{"user_id": req.UserID.String()},
		},
	}
	params.AddMetadata("user_id", req.UserID.String())

	session, err := checkoutsession.New(params)
	if err != nil {
		return nil, fmt.Errorf("failed to create checkout session: %w", err)
	}

	return &CreateCheckoutSessionResult{
		SessionID: session.ID,
		URL:       session.URL,
	}, nil
}

// CreateCustomerPortalSession creates a Stripe Customer Portal session for billing management
func (s *service) CreateCustomerPortalSession(ctx context.Context, userID uuid.UUID, returnURL string) (*CustomerPortalResult, error) {
	// Get subscription to find customer ID
	sub, err := s.repo.GetSubscriptionByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get subscription: %w", err)
	}

	if sub == nil || sub.ExternalCustomerID == nil || *sub.ExternalCustomerID == "" {
		return nil, fmt.Errorf("no Stripe customer found for user")
	}

	params := &stripe.BillingPortalSessionParams{
		Customer:  stripe.String(*sub.ExternalCustomerID),
		ReturnURL: stripe.String(returnURL),
	}

	session, err := portalsession.New(params)
	if err != nil {
		return nil, fmt.Errorf("failed to create portal session: %w", err)
	}

	return &CustomerPortalResult{
		SessionID: session.ID,
		URL:       session.URL,
	}, nil
}

// UnlimitedLimit is the sentinel the client renders as "unlimited"; the Pro
// fair-use cap is deliberately never exposed here.
const UnlimitedLimit = int32(-1)

// GetSubscription retrieves the current user's subscription with usage stats
func (s *service) GetSubscription(ctx context.Context, userID uuid.UUID) (*SubscriptionWithUsage, error) {
	sub, err := s.repo.GetSubscriptionByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get subscription: %w", err)
	}

	if sub == nil {
		// Default free tier
		sub = &Subscription{
			UserID: userID,
			Plan:   subquota.PlanFree,
			Status: "active",
		}
	}

	var requestsToday int32
	if s.quota != nil {
		usage, err := s.quota.GetDailyUsage(ctx, userID)
		if err != nil {
			// Usage display is best-effort; don't fail the whole page over it.
			s.logger.Warn("failed to read daily usage", "error", err, "user_id", userID)
		} else {
			requestsToday = int32(usage)
		}
	}

	reqLimit := int32(s.cfg.FreeDailyLimit)
	locLimit := int32(10)
	if subquota.IsProPlan(sub.Plan) {
		reqLimit = UnlimitedLimit
		locLimit = UnlimitedLimit
	}

	return &SubscriptionWithUsage{
		Subscription:        sub,
		RequestsToday:       requestsToday,
		RequestsLimit:       reqLimit,
		SavedLocations:      0,
		SavedLocationsLimit: locLimit,
	}, nil
}

// getOrCreateStripeCustomer is a helper to get existing or create new Stripe customer
func (s *service) getOrCreateStripeCustomer(email, userID string) (string, error) {
	// Search for existing customer
	params := &stripe.CustomerSearchParams{
		SearchParams: stripe.SearchParams{
			Query: fmt.Sprintf("email:'%s'", email),
		},
	}
	iter := customer.Search(params)
	if iter.Next() {
		return iter.Customer().ID, nil
	}

	// Create new customer
	custParams := &stripe.CustomerParams{
		Email: stripe.String(email),
	}
	custParams.AddMetadata("user_id", userID)

	cust, err := customer.New(custParams)
	if err != nil {
		return "", err
	}
	return cust.ID, nil
}
