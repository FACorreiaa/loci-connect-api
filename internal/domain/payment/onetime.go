package payment

import (
	"context"
	"fmt"

	stripe "github.com/stripe/stripe-go/v81"
	checkoutsession "github.com/stripe/stripe-go/v81/checkout/session"

	"github.com/FACorreiaa/loci-connect-api/pkg/observability"
)

// OneTimeCheckoutParams is a single non-recurring Checkout session.
//
// There is no mode field and the caller does not choose the price: both are
// decided here. PaymentService.CreateCheckoutSession deliberately ignores the
// mode it is sent, because a client that can name a mode and a price can name
// its own amount. This entry point is not reachable from that RPC — it is
// called by domains that resolved a price from a row the server owns.
type OneTimeCheckoutParams struct {
	UserID     string
	Email      string
	PriceID    string
	SuccessURL string
	CancelURL  string
	// Metadata is copied onto both the session and the payment intent. The
	// session is what checkout.session.completed carries, which is where
	// fulfilment reads it back.
	Metadata map[string]string
}

// CreateOneTimeCheckoutSession opens a Stripe Checkout session in payment mode.
func (s *service) CreateOneTimeCheckoutSession(ctx context.Context, p OneTimeCheckoutParams) (*CreateCheckoutSessionResult, error) {
	if p.PriceID == "" {
		return nil, fmt.Errorf("one-time checkout requires a price id")
	}

	customerID, err := s.getOrCreateStripeCustomer(p.Email, p.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to get/create customer: %w", err)
	}

	params := &stripe.CheckoutSessionParams{
		Customer:          stripe.String(customerID),
		ClientReferenceID: stripe.String(p.UserID),
		Mode:              stripe.String(string(stripe.CheckoutSessionModePayment)),
		SuccessURL:        stripe.String(p.SuccessURL),
		CancelURL:         stripe.String(p.CancelURL),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{Price: stripe.String(p.PriceID), Quantity: stripe.Int64(1)},
		},
	}
	// Metadata on the payment intent as well as the session: a refund arrives
	// as charge.refunded, which carries the intent and not the session.
	params.PaymentIntentData = &stripe.CheckoutSessionPaymentIntentDataParams{}
	for k, v := range p.Metadata {
		params.AddMetadata(k, v)
		params.PaymentIntentData.AddMetadata(k, v)
	}

	session, err := checkoutsession.New(params)
	if err != nil {
		return nil, fmt.Errorf("failed to create one-time checkout session: %w", err)
	}
	observability.CheckoutSessionsCreatedTotal.Inc()

	return &CreateCheckoutSessionResult{SessionID: session.ID, URL: session.URL}, nil
}
