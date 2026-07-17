package payment

import (
	"io"
	"log/slog"
	"net/http"

	"github.com/stripe/stripe-go/v81/webhook"
)

// WebhookHandler handles incoming Stripe webhooks. The endpoint secret comes
// from config (STRIPE_WEBHOOK_SECRET) via the caller.
func WebhookHandler(service Service, logger *slog.Logger, endpointSecret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const MaxBodyBytes = int64(65536)
		r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			logger.Error("Error reading request body", "error", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		signatureHeader := r.Header.Get("Stripe-Signature")
		event, err := webhook.ConstructEvent(payload, signatureHeader, endpointSecret)
		if err != nil {
			logger.Error("Error verifying webhook signature", "error", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if err := service.ProcessStripeEvent(r.Context(), event); err != nil {
			logger.Error("Failed to process event", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	}
}
