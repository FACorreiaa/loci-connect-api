package payment

import (
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/stripe/stripe-go/v81/webhook"
)

// WebhookHandler handles incoming Stripe webhooks
func WebhookHandler(service Service, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const MaxBodyBytes = int64(65536)
		r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			logger.Error("Error reading request body", "error", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		// Verify signature
		// In real app, load secret from config. For now getting from env for simplicity in this file scope or passed in.
		// It's better to pass it in. I'll stick to os.Getenv("STRIPE_WEBHOOK_SECRET") as per user snippet.
		endpointSecret := os.Getenv("STRIPE_WEBHOOK_SECRET")
		signatureHeader := r.Header.Get("Stripe-Signature")
		event, err := webhook.ConstructEvent(payload, signatureHeader, endpointSecret)
		if err != nil {
			logger.Error("Error verifying webhook signature", "error", err)
			w.WriteHeader(http.StatusBadRequest) // Return 400 or...
			return
		}

		// Handle the event
		if err := service.ProcessStripeEvent(r.Context(), event); err != nil {
			logger.Error("Failed to process event", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	}
}
