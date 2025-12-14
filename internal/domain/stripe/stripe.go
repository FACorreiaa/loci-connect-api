package payment

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/account"
	"github.com/stripe/stripe-go/v81/accountlink"
	"github.com/stripe/stripe-go/v81/customer"
	"github.com/stripe/stripe-go/v81/loginlink"
	"github.com/stripe/stripe-go/v81/paymentintent"
	"github.com/stripe/stripe-go/v81/price"
	"github.com/stripe/stripe-go/v81/product"
	"github.com/stripe/stripe-go/v81/refund"
	"github.com/stripe/stripe-go/v81/subscription"
)

// StripeProvider implements the PaymentProvider interface for Stripe.
type StripeProvider struct {
	apiKey string
}

// NewStripeProvider creates a new Stripe payment provider.
func NewStripeProvider(apiKey string) *StripeProvider {
	stripe.Key = apiKey
	return &StripeProvider{
		apiKey: apiKey,
	}
}

// CreatePaymentIntent creates a new payment intent in Stripe
// This supports credit cards, Apple Pay, and Google Pay automatically through Stripe's Payment Element.
func (s *StripeProvider) CreatePaymentIntent(amount int64, currency string, metadata map[string]interface{}) (string, string, error) {
	// Convert metadata to string map for Stripe
	stripeMetadata := make(map[string]string)
	for k, v := range metadata {
		stripeMetadata[k] = fmt.Sprintf("%v", v)
	}

	params := &stripe.PaymentIntentParams{
		Amount:   stripe.Int64(amount),
		Currency: stripe.String(currency),
		Metadata: stripeMetadata,
		// Automatically collect payment method details
		// This enables credit card, Apple Pay, and Google Pay
		AutomaticPaymentMethods: &stripe.PaymentIntentAutomaticPaymentMethodsParams{
			Enabled: stripe.Bool(true),
		},
	}

	pi, err := paymentintent.New(params)
	if err != nil {
		return "", "", fmt.Errorf("failed to create payment intent: %w", err)
	}

	return pi.ID, pi.ClientSecret, nil
}

// GetPaymentStatus retrieves the current status of a payment intent.
func (s *StripeProvider) GetPaymentStatus(paymentIntentID string) (string, error) {
	pi, err := paymentintent.Get(paymentIntentID, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get payment intent: %w", err)
	}

	return string(pi.Status), nil
}

// RefundPayment creates a refund for a payment
// If amount is nil, it refunds the full amount.
func (s *StripeProvider) RefundPayment(paymentIntentID string, amount *int64) error {
	params := &stripe.RefundParams{
		PaymentIntent: stripe.String(paymentIntentID),
	}

	if amount != nil {
		params.Amount = stripe.Int64(*amount)
	}

	_, err := refund.New(params)
	if err != nil {
		return fmt.Errorf("failed to create refund: %w", err)
	}

	return nil
}

// CreateCustomer creates a new Stripe customer
// This is useful for subscription management and storing payment methods.
func (s *StripeProvider) CreateCustomer(userID uuid.UUID, email string, metadata map[string]interface{}) (string, error) {
	stripeMetadata := make(map[string]string)
	for k, v := range metadata {
		stripeMetadata[k] = fmt.Sprintf("%v", v)
	}

	// Add user ID to metadata
	stripeMetadata["user_id"] = userID.String()

	params := &stripe.CustomerParams{
		Email:    stripe.String(email),
		Metadata: stripeMetadata,
	}

	c, err := customer.New(params)
	if err != nil {
		return "", fmt.Errorf("failed to create customer: %w", err)
	}

	return c.ID, nil
}

// DeleteCustomer deletes a Stripe customer
// This is useful for cleanup when subscription creation fails.
func (s *StripeProvider) DeleteCustomer(customerID string) error {
	_, err := customer.Del(customerID, nil)
	if err != nil {
		return fmt.Errorf("failed to delete customer: %w", err)
	}

	return nil
}

// CreateProduct creates a Stripe product.
func (s *StripeProvider) CreateProduct(name, description string, metadata map[string]interface{}) (string, error) {
	stripeMetadata := make(map[string]string)
	for k, v := range metadata {
		stripeMetadata[k] = fmt.Sprintf("%v", v)
	}

	params := &stripe.ProductParams{
		Name:        stripe.String(name),
		Description: stripe.String(description),
		Metadata:    stripeMetadata,
	}

	p, err := product.New(params)
	if err != nil {
		return "", fmt.Errorf("failed to create product: %w", err)
	}

	return p.ID, nil
}

// CreatePrice creates a Stripe price for a product.
func (s *StripeProvider) CreatePrice(productID string, amount int64, currency, interval string) (string, error) {
	params := &stripe.PriceParams{
		Product:    stripe.String(productID),
		UnitAmount: stripe.Int64(amount),
		Currency:   stripe.String(currency),
		Recurring: &stripe.PriceRecurringParams{
			Interval: stripe.String(interval), // "month" or "year"
		},
	}

	p, err := price.New(params)
	if err != nil {
		return "", fmt.Errorf("failed to create price: %w", err)
	}

	return p.ID, nil
}

// CreateSubscription creates a Stripe subscription for a customer.
func (s *StripeProvider) CreateSubscription(customerID, priceID string, metadata map[string]interface{}) (string, string, error) {
	stripeMetadata := make(map[string]string)
	for k, v := range metadata {
		stripeMetadata[k] = fmt.Sprintf("%v", v)
	}

	params := &stripe.SubscriptionParams{
		Customer: stripe.String(customerID),
		Items: []*stripe.SubscriptionItemsParams{
			{
				Price: stripe.String(priceID),
			},
		},
		Metadata:        stripeMetadata,
		PaymentBehavior: stripe.String("default_incomplete"),
		PaymentSettings: &stripe.SubscriptionPaymentSettingsParams{
			SaveDefaultPaymentMethod: stripe.String("on_subscription"),
		},
		// Expand the latest invoice and its payment intent to get client secret
		Expand: []*string{
			stripe.String("latest_invoice.payment_intent"),
		},
	}

	sub, err := subscription.New(params)
	if err != nil {
		return "", "", fmt.Errorf("failed to create subscription: %w", err)
	}

	// Extract client secret from the payment intent
	clientSecret := ""
	if sub.LatestInvoice != nil && sub.LatestInvoice.PaymentIntent != nil {
		clientSecret = sub.LatestInvoice.PaymentIntent.ClientSecret
	}

	return sub.ID, clientSecret, nil
}

// CancelSubscription cancels a Stripe subscription.
func (s *StripeProvider) CancelSubscription(subscriptionID string, cancelAtPeriodEnd bool) error {
	params := &stripe.SubscriptionParams{}

	if cancelAtPeriodEnd {
		params.CancelAtPeriodEnd = stripe.Bool(true)
		_, err := subscription.Update(subscriptionID, params)
		if err != nil {
			return fmt.Errorf("failed to schedule subscription cancellation: %w", err)
		}
	} else {
		_, err := subscription.Cancel(subscriptionID, nil)
		if err != nil {
			return fmt.Errorf("failed to cancel subscription: %w", err)
		}
	}

	return nil
}

// GetSubscription retrieves a Stripe subscription.
func (s *StripeProvider) GetSubscription(subscriptionID string) (*stripe.Subscription, error) {
	sub, err := subscription.Get(subscriptionID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get subscription: %w", err)
	}

	return sub, nil
}

// Stripe Connect methods for creator payouts

// CreateConnectedAccount creates a Stripe Connect Express account for a creator.
func (s *StripeProvider) CreateConnectedAccount(userID uuid.UUID, email, country string) (string, error) {
	if country == "" {
		country = "US" // Default to US
	}

	params := &stripe.AccountParams{
		Type:    stripe.String("express"), // Express accounts are easier for creators
		Email:   stripe.String(email),
		Country: stripe.String(country),
		Capabilities: &stripe.AccountCapabilitiesParams{
			CardPayments: &stripe.AccountCapabilitiesCardPaymentsParams{
				Requested: stripe.Bool(true),
			},
			Transfers: &stripe.AccountCapabilitiesTransfersParams{
				Requested: stripe.Bool(true),
			},
		},
		Metadata: map[string]string{
			"user_id": userID.String(),
		},
	}

	acct, err := account.New(params)
	if err != nil {
		return "", fmt.Errorf("failed to create connected account: %w", err)
	}

	return acct.ID, nil
}

// CreateAccountLink creates an onboarding link for a connected account.
func (s *StripeProvider) CreateAccountLink(accountID, returnURL, refreshURL string) (string, error) {
	params := &stripe.AccountLinkParams{
		Account:    stripe.String(accountID),
		RefreshURL: stripe.String(refreshURL),
		ReturnURL:  stripe.String(returnURL),
		Type:       stripe.String("account_onboarding"),
	}

	link, err := accountlink.New(params)
	if err != nil {
		return "", fmt.Errorf("failed to create account link: %w", err)
	}

	return link.URL, nil
}

// GetConnectedAccount retrieves a Stripe Connect account.
func (s *StripeProvider) GetConnectedAccount(accountID string) (*stripe.Account, error) {
	acct, err := account.GetByID(accountID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get connected account: %w", err)
	}

	return acct, nil
}

// CreateSubscriptionWithConnect creates a subscription that pays out to a connected account
// applicationFeePercent: percentage the platform takes (e.g., 10.0 for 10%).
func (s *StripeProvider) CreateSubscriptionWithConnect(
	customerID, priceID string,
	metadata map[string]interface{},
	connectedAccountID string,
	applicationFeePercent float64,
) (string, string, error) {
	stripeMetadata := make(map[string]string)
	for k, v := range metadata {
		stripeMetadata[k] = fmt.Sprintf("%v", v)
	}

	params := &stripe.SubscriptionParams{
		Customer: stripe.String(customerID),
		Items: []*stripe.SubscriptionItemsParams{
			{
				Price: stripe.String(priceID),
			},
		},
		Metadata:        stripeMetadata,
		PaymentBehavior: stripe.String("default_incomplete"),
		PaymentSettings: &stripe.SubscriptionPaymentSettingsParams{
			SaveDefaultPaymentMethod: stripe.String("on_subscription"),
		},
		// Transfer funds to creator, platform takes fee
		TransferData: &stripe.SubscriptionTransferDataParams{
			Destination:   stripe.String(connectedAccountID),
			AmountPercent: stripe.Float64(100.0 - applicationFeePercent),
		},
		ApplicationFeePercent: stripe.Float64(applicationFeePercent),
		// Expand the latest invoice and its payment intent to get client secret
		Expand: []*string{
			stripe.String("latest_invoice.payment_intent"),
		},
	}

	sub, err := subscription.New(params)
	if err != nil {
		return "", "", fmt.Errorf("failed to create subscription with connect: %w", err)
	}

	// Extract client secret from the payment intent
	clientSecret := ""
	if sub.LatestInvoice != nil && sub.LatestInvoice.PaymentIntent != nil {
		clientSecret = sub.LatestInvoice.PaymentIntent.ClientSecret
	}

	return sub.ID, clientSecret, nil
}

// CreateLoginLink creates a Stripe Express Dashboard login link for a connected account.
func (s *StripeProvider) CreateLoginLink(accountID string) (string, error) {
	params := &stripe.LoginLinkParams{
		Account: stripe.String(accountID),
	}

	link, err := loginlink.New(params)
	if err != nil {
		return "", fmt.Errorf("failed to create login link: %w", err)
	}

	return link.URL, nil
}
