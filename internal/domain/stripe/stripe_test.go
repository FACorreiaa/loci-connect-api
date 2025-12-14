//go:build stripe_tests

package payment

import (
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStripeProvider(t *testing.T) {
	apiKey := "sk_test_123"
	provider := NewStripeProvider(apiKey)

	assert.NotNil(t, provider)
	assert.Equal(t, apiKey, provider.apiKey)
}

func TestStripeProvider_CreatePaymentIntent(t *testing.T) {
	// Skip if no Stripe API key is set
	apiKey := os.Getenv("STRIPE_TEST_API_KEY")
	if apiKey == "" {
		t.Skip("STRIPE_TEST_API_KEY not set, skipping integration test")
	}

	provider := NewStripeProvider(apiKey)

	t.Run("successful payment intent creation", func(t *testing.T) {
		metadata := map[string]interface{}{
			"user_id": uuid.New().String(),
			"test":    "data",
		}

		paymentIntentID, clientSecret, err := provider.CreatePaymentIntent(1000, "usd", metadata)

		require.NoError(t, err)
		assert.NotEmpty(t, paymentIntentID)
		assert.NotEmpty(t, clientSecret)
		assert.Contains(t, paymentIntentID, "pi_")
	})

	t.Run("invalid currency", func(t *testing.T) {
		metadata := map[string]interface{}{
			"user_id": uuid.New().String(),
		}

		_, _, err := provider.CreatePaymentIntent(1000, "invalid", metadata)
		assert.Error(t, err)
	})

	t.Run("zero amount", func(t *testing.T) {
		metadata := map[string]interface{}{
			"user_id": uuid.New().String(),
		}

		_, _, err := provider.CreatePaymentIntent(0, "usd", metadata)
		assert.Error(t, err)
	})

	t.Run("with nil metadata", func(t *testing.T) {
		paymentIntentID, clientSecret, err := provider.CreatePaymentIntent(1000, "usd", nil)

		require.NoError(t, err)
		assert.NotEmpty(t, paymentIntentID)
		assert.NotEmpty(t, clientSecret)
	})
}

func TestStripeProvider_GetPaymentStatus(t *testing.T) {
	apiKey := os.Getenv("STRIPE_TEST_API_KEY")
	if apiKey == "" {
		t.Skip("STRIPE_TEST_API_KEY not set, skipping integration test")
	}

	provider := NewStripeProvider(apiKey)

	t.Run("get status of existing payment", func(t *testing.T) {
		// First create a payment intent
		metadata := map[string]interface{}{
			"user_id": uuid.New().String(),
		}
		paymentIntentID, _, err := provider.CreatePaymentIntent(1000, "usd", metadata)
		require.NoError(t, err)

		// Get its status
		status, err := provider.GetPaymentStatus(paymentIntentID)
		require.NoError(t, err)
		assert.NotEmpty(t, status)
		// Status should be one of the Stripe statuses
		assert.Contains(t, []string{"requires_payment_method", "requires_confirmation", "requires_action", "processing", "requires_capture", "canceled", "succeeded"}, status)
	})

	t.Run("invalid payment intent ID", func(t *testing.T) {
		_, err := provider.GetPaymentStatus("pi_invalid")
		assert.Error(t, err)
	})
}

func TestStripeProvider_RefundPayment(t *testing.T) {
	apiKey := os.Getenv("STRIPE_TEST_API_KEY")
	if apiKey == "" {
		t.Skip("STRIPE_TEST_API_KEY not set, skipping integration test")
	}

	provider := NewStripeProvider(apiKey)

	t.Run("refund non-existent payment", func(t *testing.T) {
		// This should fail since the payment doesn't exist or isn't paid
		err := provider.RefundPayment("pi_nonexistent", nil)
		assert.Error(t, err)
	})

	t.Run("partial refund amount", func(t *testing.T) {
		// This will fail since we can't actually charge a card in tests
		// but it tests the code path
		amount := int64(500)
		err := provider.RefundPayment("pi_test", &amount)
		assert.Error(t, err) // Expected to fail since payment doesn't exist
	})
}

func TestStripeProvider_CreateCustomer(t *testing.T) {
	apiKey := os.Getenv("STRIPE_TEST_API_KEY")
	if apiKey == "" {
		t.Skip("STRIPE_TEST_API_KEY not set, skipping integration test")
	}

	provider := NewStripeProvider(apiKey)

	t.Run("successful customer creation", func(t *testing.T) {
		userID := uuid.New()
		email := "test@example.com"
		metadata := map[string]interface{}{
			"app": "fanapi",
		}

		customerID, err := provider.CreateCustomer(userID, email, metadata)

		require.NoError(t, err)
		assert.NotEmpty(t, customerID)
		assert.Contains(t, customerID, "cus_")
	})

	t.Run("customer with nil metadata", func(t *testing.T) {
		userID := uuid.New()
		email := "test2@example.com"

		customerID, err := provider.CreateCustomer(userID, email, nil)

		require.NoError(t, err)
		assert.NotEmpty(t, customerID)
		assert.Contains(t, customerID, "cus_")
	})

	t.Run("customer with empty email", func(t *testing.T) {
		userID := uuid.New()
		metadata := map[string]interface{}{}

		customerID, err := provider.CreateCustomer(userID, "", metadata)

		// Stripe allows customers without email
		require.NoError(t, err)
		assert.NotEmpty(t, customerID)
	})
}

func TestStripeProvider_CreateSubscriptionPayment(t *testing.T) {
	t.Skip("CreateSubscriptionPayment is a placeholder method and requires amount/currency parameters - skipping until full Stripe Subscription API is implemented")

	apiKey := os.Getenv("STRIPE_TEST_API_KEY")
	if apiKey == "" {
		t.Skip("STRIPE_TEST_API_KEY not set, skipping integration test")
	}

	provider := NewStripeProvider(apiKey)

	t.Run("create subscription payment intent", func(t *testing.T) {
		// First create a customer
		userID := uuid.New()
		email := "subscription@example.com"
		customerID, err := provider.CreateCustomer(userID, email, nil)
		require.NoError(t, err)

		// Create subscription
		metadata := map[string]interface{}{
			"subscription": "premium",
		}
		subscriptionID, clientSecret, err := provider.CreateSubscription(customerID, "price_test", metadata)

		require.NoError(t, err)
		assert.NotEmpty(t, subscriptionID)
		assert.NotEmpty(t, clientSecret)
		assert.Contains(t, subscriptionID, "sub_")
	})

	t.Run("invalid customer ID", func(t *testing.T) {
		metadata := map[string]interface{}{}
		_, _, err := provider.CreateSubscription("cus_invalid", "price_test", metadata)
		assert.Error(t, err)
	})
}

// Unit tests with mock data (no API calls).
func TestStripeProvider_Unit(t *testing.T) {
	provider := NewStripeProvider("sk_test_mock")

	t.Run("provider initialized correctly", func(t *testing.T) {
		assert.NotNil(t, provider)
		assert.Equal(t, "sk_test_mock", provider.apiKey)
	})
}
