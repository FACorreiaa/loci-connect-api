package telegram

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
)

// secretHeader is the header Telegram echoes back the secret_token given to
// setWebhook. It is the only thing distinguishing a real delivery from anyone
// who guesses the URL, so the endpoint is worthless without it.
const secretHeader = "X-Telegram-Bot-Api-Secret-Token" //nolint:gosec // header name, not a credential

// maxUpdateBytes bounds one update. Telegram's are small; this is well above
// the largest real one and far below anything worth buffering from a stranger.
const maxUpdateBytes = 1 << 20

// Webhook serves Telegram's POSTs.
//
// Mounted outside the Connect interceptor chain, for the reason /webhooks/stripe
// is: the caller is not a browser and holds no JWT; it authenticates with a
// shared secret instead.
type Webhook struct {
	bridge bridge
	secret string
	logger *slog.Logger
}

// NewWebhook builds the handler. Returns nil when no secret is configured,
// which is the caller's signal not to mount the route at all: an endpoint
// with an empty secret would accept anything, and refusing to build one is
// how that state is made unreachable.
func NewWebhook(client *Client, handler Handler, secret string, logger *slog.Logger) *Webhook {
	if secret == "" || client == nil || handler == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Webhook{
		bridge: bridge{client: client, handler: handler, logger: logger},
		secret: secret,
		logger: logger,
	}
}

func (h *Webhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Constant time, because a byte-at-a-time comparison against a secret is
	// measurable over enough requests and this endpoint is public.
	presented := r.Header.Get(secretHeader)
	if subtle.ConstantTimeCompare([]byte(presented), []byte(h.secret)) != 1 {
		h.logger.WarnContext(r.Context(), "telegram webhook rejected an unauthenticated delivery")
		// No body: anything descriptive here tells a prober how close they are.
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// One byte past the cap is read so that "too large" can be told apart
	// from "exactly at the cap"; a truncated update would otherwise decode as
	// a different, smaller message or fail with a misleading parse error.
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxUpdateBytes+1))
	if err != nil {
		http.Error(w, "could not read update", http.StatusBadRequest)
		return
	}
	if len(raw) > maxUpdateBytes {
		http.Error(w, "update too large", http.StatusRequestEntityTooLarge)
		return
	}

	var update Update
	if err := json.Unmarshal(raw, &update); err != nil {
		http.Error(w, "not a telegram update", http.StatusBadRequest)
		return
	}

	// Acknowledged before it is answered. Telegram retries anything that is not
	// a prompt 200 and an itinerary takes minutes, so holding the request open
	// would guarantee both a timeout and a redelivery. A redelivery that
	// happens anyway is answered twice, which is the accepted cost.
	//
	// The request context is cancelled the moment this handler returns, so the
	// answer runs on a copy with the cancellation removed and the values —
	// request id, trace — kept.
	w.WriteHeader(http.StatusOK)
	go h.bridge.handle(context.WithoutCancel(r.Context()), update)
}
