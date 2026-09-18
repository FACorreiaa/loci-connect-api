package bundle

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/payment"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// CheckoutAdapter adapts the payment service to what this domain needs.
//
// It lives here rather than in payment so that payment stays unaware of City
// Packs: it offers a generic one-time session and learns nothing about what is
// being sold.
type CheckoutAdapter struct {
	svc payment.Service
}

// NewCheckoutAdapter wraps a payment service.
func NewCheckoutAdapter(svc payment.Service) *CheckoutAdapter {
	return &CheckoutAdapter{svc: svc}
}

var (
	_ CheckoutStarter = (*CheckoutAdapter)(nil)
	// Service satisfies payment.PurchaseFulfiller structurally. Asserting it
	// here means a signature drift is a compile error rather than a webhook
	// that silently stops granting what people paid for.
	_ payment.PurchaseFulfiller = (*Service)(nil)
)

// CreateOneTimeCheckoutSession opens the Stripe session for a pack.
func (a *CheckoutAdapter) CreateOneTimeCheckoutSession(ctx context.Context, p OneTimeCheckoutParams) (string, string, error) {
	res, err := a.svc.CreateOneTimeCheckoutSession(ctx, payment.OneTimeCheckoutParams{
		UserID:     p.UserID.String(),
		Email:      p.Email,
		PriceID:    p.PriceID,
		SuccessURL: p.SuccessURL,
		CancelURL:  p.CancelURL,
		Metadata:   p.Metadata,
	})
	if err != nil {
		return "", "", err
	}
	return res.SessionID, res.URL, nil
}

// FulfilPurchase grants a pack after Stripe confirms payment.
//
// This is the method payment.PurchaseFulfiller asks for. The signature uses
// only standard types on purpose: it lets this service satisfy that interface
// without importing it back, so the two packages stay acyclic.
//
// A kind that is not ours is ignored rather than refused. Other one-time
// products may exist later, and a webhook that fails is a webhook Stripe
// retries forever.
func (s *Service) FulfilPurchase(
	ctx context.Context,
	kind string,
	meta map[string]string,
	sessionID, paymentIntentID string,
	amountCents int64,
	currency string,
) error {
	if kind != PurchaseKindCityPack {
		return nil
	}

	userID, err := uuid.Parse(meta["user_id"])
	if err != nil {
		return fmt.Errorf("city pack purchase with unusable user id %q: %w", meta["user_id"], err)
	}
	bundleID, err := uuid.Parse(meta["bundle_id"])
	if err != nil {
		return fmt.Errorf("city pack purchase with unusable bundle id %q: %w", meta["bundle_id"], err)
	}
	if sessionID == "" {
		return errors.New("city pack purchase with no checkout session id")
	}
	if currency == "" {
		currency = s.Currency()
	}

	return s.Fulfil(ctx, Purchase{
		UserID:                  userID,
		BundleID:                bundleID,
		StripeCheckoutSessionID: sessionID,
		StripePaymentIntentID:   paymentIntentID,
		AmountCents:             int(amountCents),
		Currency:                currency,
	})
}

// RevokePurchase withdraws a pack after a refund.
func (s *Service) RevokePurchase(ctx context.Context, paymentIntentID string) error {
	return s.Refund(ctx, paymentIntentID)
}

// UserEmailLookup resolves a buyer's email from the user repository.
type UserEmailLookup struct {
	users interface {
		GetUserByID(ctx context.Context, userID uuid.UUID) (*locitypes.UserProfile, error)
	}
}

// NewUserEmailLookup wraps a user repository.
func NewUserEmailLookup(users interface {
	GetUserByID(ctx context.Context, userID uuid.UUID) (*locitypes.UserProfile, error)
},
) *UserEmailLookup {
	return &UserEmailLookup{users: users}
}

var _ EmailLookup = (*UserEmailLookup)(nil)

// EmailForUser returns the address Stripe should bill.
func (l *UserEmailLookup) EmailForUser(ctx context.Context, userID uuid.UUID) (string, error) {
	u, err := l.users.GetUserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	if u == nil {
		return "", errors.New("user not found")
	}
	return u.Email, nil
}
