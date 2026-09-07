package aicreds

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/genai"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"github.com/FACorreiaa/loci-connect-api/pkg/ai"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Router is a ChatClient that picks, per call, between the caller's own
// provider and the one Loci runs.
//
// It is a wrapper rather than a change at each call site because the services
// reach their client from about a dozen places, most of which have no user id
// in scope — threading one through all of them to express "whose key is this"
// would be a far larger change than the feature. Every method on the interface
// that could need the answer already takes a context, and the context already
// carries the authenticated caller.
//
// The cost is that the choice is implicit. What keeps that honest: a caller
// with no credential, an unauthenticated context, and a credential that will
// not open all resolve to the shared client, so the failure mode is "runs on
// Loci's provider", never "fails".
type Router struct {
	shared generativeAI.ChatClient
	svc    *Service
	logger *slog.Logger

	// httpClient is shared by every user's client so a per-user provider gets
	// connection reuse instead of a TLS handshake per request. Gateway
	// providers wrap it in a dialer that re-checks the target; see
	// providers.GatewayHTTPClient.
	httpClient *http.Client

	mu     sync.Mutex
	cached map[uuid.UUID]*cachedClient
}

type cachedClient struct {
	client generativeAI.ChatClient
	// version is the credential's updated_at when this client was built. A
	// saved change moves it, which is what makes the next request pick up the
	// new key rather than the next deploy.
	version time.Time
}

var _ generativeAI.ChatClient = (*Router)(nil)

// NewRouter wraps shared so that users who brought their own key are served by
// it. A nil service, or one with no encryption key configured, yields a router
// that always returns shared — which is every account today.
func NewRouter(shared generativeAI.ChatClient, svc *Service, logger *slog.Logger) *Router {
	if logger == nil {
		logger = slog.Default()
	}
	return &Router{
		shared:     shared,
		svc:        svc,
		logger:     logger,
		httpClient: &http.Client{},
		cached:     make(map[uuid.UUID]*cachedClient),
	}
}

// clientFor answers which client serves this call.
//
// Every failure here returns the shared client. A broken credential must
// degrade the account to Loci's provider, not break it: the alternative is
// that one bad key saved in settings makes every itinerary request fail.
func (r *Router) clientFor(ctx context.Context) generativeAI.ChatClient {
	if r.svc == nil || !r.svc.Enabled() {
		return r.shared
	}

	rawID, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok {
		// Background work and unauthenticated paths run on Loci's provider.
		return r.shared
	}
	userID, err := uuid.Parse(rawID)
	if err != nil {
		return r.shared
	}

	resolved, err := r.svc.Resolve(ctx, userID)
	switch {
	case errors.Is(err, ErrNotFound):
		// The ordinary case: this account did not bring a key.
		return r.shared
	case err != nil:
		// A stored credential that will not open — the encryption key was
		// rotated out from under it, or the address it names is no longer one
		// Loci will dial. Recorded, not only logged: this is the failure the
		// owner can actually fix, and without a note the settings page would
		// go on showing their key as in use while every request quietly ran on
		// ours.
		r.recordFailure(ctx, userID, err)
		return r.shared
	}

	client, err := r.cachedFor(ctx, userID, resolved)
	if err != nil {
		r.recordFailure(ctx, userID, err)
		return r.shared
	}
	return client
}

// recordFailure notes why this credential did not serve the request.
//
// Best effort, and deliberately not fatal: it runs on a path that has already
// fallen back, and a database that will not take the note must not also break
// the request that was about to succeed on the shared provider.
func (r *Router) recordFailure(ctx context.Context, userID uuid.UUID, cause error) {
	r.logger.WarnContext(ctx, "falling back to the shared provider",
		slog.String("user_id", userID.String()),
		slog.String("error", cause.Error()))

	if err := r.svc.RecordFailure(ctx, userID, cause.Error()); err != nil {
		r.logger.WarnContext(ctx, "could not record provider failure",
			slog.String("user_id", userID.String()),
			slog.String("error", err.Error()))
	}
}

// cachedFor returns a client for this credential, building one if the cached
// entry is missing or stale.
//
// Clients are cached because building one is not free — a Gemini client opens
// its own transport, and one built per request would be a leak as much as an
// expense.
func (r *Router) cachedFor(ctx context.Context, userID uuid.UUID, resolved Resolved) (generativeAI.ChatClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if entry, ok := r.cached[userID]; ok {
		if entry.version.Equal(resolved.Version) {
			return entry.client, nil
		}
		// The credential changed. Close the client built from the old one
		// before dropping it, or its connections outlive the key.
		if err := entry.client.Close(); err != nil {
			r.logger.WarnContext(ctx, "could not close a replaced provider client",
				slog.String("error", err.Error()))
		}
		delete(r.cached, userID)
	}

	client, err := ai.NewUserChatClient(ctx, ai.UserSpec{
		Provider:   resolved.Provider,
		Model:      resolved.Model,
		BaseURL:    resolved.BaseURL,
		APIKey:     resolved.APIKey,
		HTTPClient: r.httpClient,
		Logger:     r.logger,
	})
	if err != nil {
		return nil, err
	}

	r.cached[userID] = &cachedClient{client: client, version: resolved.Version}
	return client, nil
}

// Forget drops the cached client for a user, so a credential deleted through
// settings stops serving requests immediately rather than at its next change.
func (r *Router) Forget(userID uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if entry, ok := r.cached[userID]; ok {
		_ = entry.client.Close()
		delete(r.cached, userID)
	}
}

func (r *Router) Generate(ctx context.Context, prompt string, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	return r.clientFor(ctx).Generate(ctx, prompt, config)
}

func (r *Router) GenerateText(ctx context.Context, prompt string, config *genai.GenerateContentConfig) (string, error) {
	return r.clientFor(ctx).GenerateText(ctx, prompt, config)
}

func (r *Router) GenerateStream(ctx context.Context, prompt string, config *genai.GenerateContentConfig) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
	return r.clientFor(ctx).GenerateStream(ctx, prompt, config)
}

func (r *Router) StartChatSession(ctx context.Context, config *genai.GenerateContentConfig) (*generativeAI.ChatSession, error) {
	return r.clientFor(ctx).StartChatSession(ctx, config)
}

// Model reports the shared provider's model.
//
// The interface gives it no context, so there is no caller to resolve. It is
// read for logging and metrics rather than to route a request, and answering
// with the shared model is better than guessing at a user's.
func (r *Router) Model() string { return r.shared.Model() }

// Close closes the shared client and every client built for a user.
func (r *Router) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var errs []error
	for userID, entry := range r.cached {
		if err := entry.client.Close(); err != nil {
			errs = append(errs, err)
		}
		delete(r.cached, userID)
	}
	if err := r.shared.Close(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
